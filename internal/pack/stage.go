// Package pack fetches packs into a staging area, normalizes their skill
// directories, and moves them into place. Every subprocess lives in the
// adapter files, so the rest of the program never runs external commands.
package pack

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// Directory names inside the library root.
const (
	ImportDir  = ".import"
	RemovedDir = ".removed"
	PacksDir   = "packs"
	// SwapPrefix marks a tombstone directory in RemovedDir.
	SwapPrefix = ".swap-"
)

// LargePackBytes is the size above which `add` warns.
const LargePackBytes = 25 << 20

// Spec says what to fetch.
type Spec struct {
	Name   string
	Fetch  manifest.Fetch
	Source string // local path or npx package spec
	URL    string // git URL
	Ref    string
	// Path is a subtree hint for git, or the source directory for local.
	Path string
}

// Staged is a fetched and normalized pack, not yet in place.
type Staged struct {
	// Dir is the slash path of the staged pack directory, relative to the root.
	Dir string
	// Work is the slash path of the whole staging directory to clean up.
	Work string
	// Resolved is the commit that the fetch produced. Empty for a local pack.
	Resolved string
	// Path is the subtree that held the skills (git only).
	Path string
	// Skills holds the normalized skill names, sorted.
	Skills []string
	// Renamed maps an original directory name to its normalized name.
	Renamed map[string]string
	// Size is the total size of the staged files, in bytes.
	Size int64
	// Notes are messages for the user about what the fetch did or left out.
	Notes []string
	// LockEntries holds raw skills-lock.json entries (npx only).
	LockEntries map[string][]byte
}

// newWorkDir creates .import/<name>-<random> and returns its slash path.
func newWorkDir(root *os.Root, name string) (string, error) {
	if err := root.MkdirAll(ImportDir, 0o755); err != nil {
		return "", err
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	work := path.Join(ImportDir, name+"-"+hex.EncodeToString(b[:]))
	if err := root.Mkdir(work, 0o755); err != nil {
		return "", err
	}
	return work, nil
}

// Cleanup removes a staging directory.
func Cleanup(root *os.Root, s *Staged) {
	if s != nil && s.Work != "" {
		root.RemoveAll(s.Work)
	}
}

// normalizeInto moves or copies the skill directories found directly under
// srcDir into dstDir with normalized names. A directory is a skill when it has
// a SKILL.md file. It rejects collisions and names that cannot be normalized.
// srcDir is a path on disk; for a rename it must be inside the library.
func normalizeInto(src fs.FS, srcDir string, dst func(orig, norm string) error) (skills []string, renamed map[string]string, err error) {
	entries, err := fs.ReadDir(src, srcDir)
	if err != nil {
		return nil, nil, err
	}
	byNorm := map[string][]string{}
	renamed = map[string]string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !hasSkillFile(src, path.Join(srcDir, e.Name())) {
			continue
		}
		norm := library.NormalizeName(e.Name())
		if !manifest.NameRE.MatchString(norm) {
			return nil, nil, fmt.Errorf("skill directory %q cannot be normalized to a valid name (got %q)", e.Name(), norm)
		}
		byNorm[norm] = append(byNorm[norm], e.Name())
		if norm != e.Name() {
			renamed[e.Name()] = norm
		}
	}
	var collisions []string
	for norm, origs := range byNorm {
		if len(origs) > 1 {
			sort.Strings(origs)
			collisions = append(collisions, fmt.Sprintf("%s <- %s", norm, strings.Join(origs, ", ")))
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return nil, nil, fmt.Errorf("skill names collide after normalization: %s", strings.Join(collisions, "; "))
	}
	if len(byNorm) == 0 {
		return nil, nil, errors.New("no skills found: no directory with a SKILL.md")
	}
	for norm, origs := range byNorm {
		if err := dst(origs[0], norm); err != nil {
			return nil, nil, err
		}
		skills = append(skills, norm)
	}
	sort.Strings(skills)
	return skills, renamed, nil
}

// treeSize sums the sizes of the regular files under dir.
func treeSize(root *os.Root, dir string) (int64, error) {
	var total int64
	var walk func(rel string) error
	walk = func(rel string) error {
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		items, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, it := range items {
			switch {
			case it.IsDir():
				if err := walk(path.Join(rel, it.Name())); err != nil {
					return err
				}
			case it.Type().IsRegular():
				info, err := it.Info()
				if err != nil {
					return err
				}
				total += info.Size()
			}
		}
		return nil
	}
	return total, walk(dir)
}

// Exists reports whether a path exists inside the root, without following a
// final symlink.
func Exists(root *os.Root, rel string) bool {
	_, err := root.Lstat(rel)
	return err == nil
}

// Swap moves the staged pack into packs/<name>. An existing pack is renamed to
// a tombstone in .removed first, so a concurrent reader sees entirely old
// content, a missing pack, or entirely new content, never a partial tree. Swap
// deletes the tombstone.
func Swap(root *os.Root, s *Staged, name string) error {
	tomb, err := SwapKeep(root, s, name)
	if err != nil {
		return err
	}
	return DropTombstone(root, tomb)
}

// SwapKeep is Swap, but it keeps the tombstone and returns its path ("" when
// there was no old pack). The caller deletes it with DropTombstone when the lock
// and manifest are written, or puts the old pack back with RestoreTombstone.
func SwapKeep(root *os.Root, s *Staged, name string) (string, error) {
	dest := path.Join(PacksDir, name)
	if err := root.MkdirAll(PacksDir, 0o755); err != nil {
		return "", err
	}
	if !Exists(root, dest) {
		return "", root.Rename(s.Dir, dest)
	}
	if err := root.MkdirAll(RemovedDir, 0o755); err != nil {
		return "", err
	}
	tomb := path.Join(RemovedDir, fmt.Sprintf("%s%s-%d", SwapPrefix, name, time.Now().UnixNano()))
	if err := root.Rename(dest, tomb); err != nil {
		return "", err
	}
	if err := root.Rename(s.Dir, dest); err != nil {
		root.Rename(tomb, dest) // put the old pack back
		return "", err
	}
	return tomb, nil
}

// DropTombstone deletes a tombstone. An empty path is a no-op.
func DropTombstone(root *os.Root, tomb string) error {
	if tomb == "" {
		return nil
	}
	return root.RemoveAll(tomb)
}

// RestoreTombstone replaces packs/<name> with the old pack in the tombstone.
func RestoreTombstone(root *os.Root, tomb, name string) error {
	if tomb == "" {
		return nil
	}
	dest := path.Join(PacksDir, name)
	if err := root.RemoveAll(dest); err != nil {
		return err
	}
	return root.Rename(tomb, dest)
}

var tombRE = regexp.MustCompile(`^` + regexp.QuoteMeta(SwapPrefix) + `(.+)-\d+$`)

// TombstonePack returns the pack name that a tombstone directory name holds.
func TombstonePack(dirName string) (string, bool) {
	m := tombRE.FindStringSubmatch(dirName)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// RecoverTombstones puts back the old pack from a tombstone when the pack
// directory is missing. A crash between the two renames of a swap leaves that
// state, and the tombstone then holds the only copy. It returns the packs that
// it recovered.
func RecoverTombstones(root *os.Root, known func(name string) bool) ([]string, error) {
	f, err := root.Open(RemovedDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var recovered []string
	for _, e := range entries {
		name, ok := TombstonePack(e.Name())
		if !ok || !known(name) || Exists(root, path.Join(PacksDir, name)) {
			continue
		}
		if err := root.MkdirAll(PacksDir, 0o755); err != nil {
			return recovered, err
		}
		if err := root.Rename(path.Join(RemovedDir, e.Name()), path.Join(PacksDir, name)); err != nil {
			return recovered, err
		}
		recovered = append(recovered, name)
	}
	return recovered, nil
}

// CheckLinks rejects a staged pack that holds a symlink which leaves its skill
// directory. A skill may link to its own files and to nothing else, because the
// agent follows links when it reads supporting files. This is stricter than the
// pack directory that the shim lets the agent read: a path in a body is in plain
// view, but a link is followed with no sign.
func CheckLinks(root *os.Root, dir string) error {
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	skills, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	var bad []string
	for _, sk := range skills {
		top := path.Join(dir, sk.Name())
		if sk.Type()&fs.ModeSymlink != 0 {
			target, _ := root.Readlink(top)
			bad = append(bad, sk.Name()+" -> "+target)
			continue
		}
		var walk func(rel string) error
		walk = func(rel string) error {
			d, err := root.Open(rel)
			if err != nil {
				return err
			}
			items, err := d.ReadDir(-1)
			d.Close()
			if err != nil {
				return err
			}
			for _, it := range items {
				p := path.Join(rel, it.Name())
				switch {
				case it.Type()&fs.ModeSymlink != 0:
					target, err := root.Readlink(p)
					if err != nil || !linkStaysInside(top, p, target) {
						bad = append(bad, strings.TrimPrefix(p, dir+"/")+" -> "+target)
					}
				case it.IsDir():
					if err := walk(p); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if sk.IsDir() {
			if err := walk(top); err != nil {
				return err
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("the pack has symlinks that leave their skill directory: %s", strings.Join(bad, ", "))
	}
	return nil
}

// Quarantine moves packs/<name> to .removed/<name>. When that name is taken,
// it adds a timestamp, so an older quarantine is never overwritten.
func Quarantine(root *os.Root, name string) (string, error) {
	return quarantine(root, path.Join(PacksDir, name), name)
}

func quarantine(root *os.Root, from, name string) (string, error) {
	if err := root.MkdirAll(RemovedDir, 0o755); err != nil {
		return "", err
	}
	dest := path.Join(RemovedDir, name)
	if Exists(root, dest) {
		dest = fmt.Sprintf("%s.%d", dest, time.Now().Unix())
	}
	if err := root.Rename(from, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// QuarantineOrphan moves an unclaimed packs/<name> directory to .removed.
func QuarantineOrphan(root *os.Root, name string) (string, error) {
	return Quarantine(root, name)
}
