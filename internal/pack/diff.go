package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
)

// Diff summarizes the change between two pack trees.
type Diff struct {
	AddedSkills   []string
	RemovedSkills []string
	Added         []string
	Removed       []string
	Changed       []string
}

// Empty reports whether the trees hold the same files.
func (d *Diff) Empty() bool { return len(d.Added)+len(d.Removed)+len(d.Changed) == 0 }

// Scripts returns the changed, added, or removed paths that look like scripts.
func (d *Diff) Scripts() []string {
	var out []string
	for _, group := range [][]string{d.Added, d.Changed, d.Removed} {
		for _, p := range group {
			if i := strings.IndexByte(p, '/'); i > 0 && isScriptPath(p[i+1:]) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func isScriptPath(rel string) bool {
	return library.IsScript(rel, 0)
}

// DiffTrees compares two directories inside the root. A missing oldDir counts
// as empty.
func DiffTrees(root *os.Root, oldDir, newDir string) (*Diff, error) {
	oldFiles, err := fingerprint(root, oldDir)
	if err != nil {
		return nil, err
	}
	newFiles, err := fingerprint(root, newDir)
	if err != nil {
		return nil, err
	}
	d := &Diff{}
	skills := func(m map[string]string) map[string]bool {
		s := map[string]bool{}
		for p := range m {
			if i := strings.IndexByte(p, '/'); i > 0 {
				s[p[:i]] = true
			}
		}
		return s
	}
	oldSk, newSk := skills(oldFiles), skills(newFiles)
	for s := range newSk {
		if !oldSk[s] {
			d.AddedSkills = append(d.AddedSkills, s)
		}
	}
	for s := range oldSk {
		if !newSk[s] {
			d.RemovedSkills = append(d.RemovedSkills, s)
		}
	}
	for p, h := range newFiles {
		switch old, ok := oldFiles[p]; {
		case !ok:
			d.Added = append(d.Added, p)
		case old != h:
			d.Changed = append(d.Changed, p)
		}
	}
	for p := range oldFiles {
		if _, ok := newFiles[p]; !ok {
			d.Removed = append(d.Removed, p)
		}
	}
	for _, s := range [][]string{d.AddedSkills, d.RemovedSkills, d.Added, d.Removed, d.Changed} {
		sort.Strings(s)
	}
	return d, nil
}

// fingerprint maps each file path to a hash of its content and mode.
func fingerprint(root *os.Root, dir string) (map[string]string, error) {
	out := map[string]string{}
	if _, err := root.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var walk func(rel string) error
	walk = func(rel string) error {
		f, err := root.Open(path.Join(dir, rel))
		if err != nil {
			return err
		}
		items, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, it := range items {
			if excludedName(it.Name()) {
				continue
			}
			p := path.Join(rel, it.Name())
			full := path.Join(dir, p)
			switch {
			case it.Type()&fs.ModeSymlink != 0:
				t, err := root.Readlink(full)
				if err != nil {
					return err
				}
				out[p] = "l:" + t
			case it.IsDir():
				if err := walk(p); err != nil {
					return err
				}
			case it.Type().IsRegular():
				info, err := it.Info()
				if err != nil {
					return err
				}
				h := sha256.New()
				in, err := root.Open(full)
				if err != nil {
					return err
				}
				_, err = io.Copy(h, in)
				in.Close()
				if err != nil {
					return err
				}
				out[p] = fmt.Sprintf("f:%o:%s", info.Mode()&0o111, hex.EncodeToString(h.Sum(nil)))
			}
		}
		return nil
	}
	return out, walk("")
}

func excludedName(n string) bool {
	return n == ".git" || n == ".DS_Store" || n == ".import" || n == ".removed"
}
