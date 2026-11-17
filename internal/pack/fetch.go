package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// Fetch stages a pack: it runs the adapter for spec.Fetch, normalizes the skill
// directory names into .import/<id>/pack, and reports what it found. The
// library is not changed outside .import.
func Fetch(root *os.Root, spec Spec) (*Staged, error) {
	work, err := newWorkDir(root, spec.Name)
	if err != nil {
		return nil, err
	}
	var s *Staged
	switch spec.Fetch {
	case manifest.FetchLocal:
		s, err = fetchLocal(root, work, spec)
	case manifest.FetchGit:
		s, err = fetchGit(root, work, spec)
	case manifest.FetchNPX:
		s, err = fetchNPX(root, work, spec)
	default:
		err = fmt.Errorf("unknown fetch %q", spec.Fetch)
	}
	if err != nil {
		root.RemoveAll(work)
		return nil, err
	}
	s.Work = work
	if err := CheckLinks(root, s.Dir); err != nil {
		root.RemoveAll(work)
		return nil, err
	}
	size, err := treeSize(root, s.Dir)
	if err != nil {
		root.RemoveAll(work)
		return nil, err
	}
	s.Size = size
	return s, nil
}

// normalizeMove renames skill directories from srcDir (inside the root) into
// work/pack with normalized names.
//
// srcDir must lie inside the directory base, with no symlink that leaves base.
// A fetched repo can hold a symlink such as skills -> ../../../skills, and
// os.Root follows links that stay inside the library, so without this check a
// pack could take the library's own skills.
func normalizeMove(root *os.Root, work, base, srcDir string) (*Staged, error) {
	if err := confine(root, base, srcDir); err != nil {
		return nil, err
	}
	dstDir := path.Join(work, "pack")
	if err := root.Mkdir(dstDir, 0o755); err != nil {
		return nil, err
	}
	skills, renamed, err := normalizeInto(root.FS(), srcDir, func(orig, norm string) error {
		return root.Rename(path.Join(srcDir, orig), path.Join(dstDir, norm))
	})
	if err != nil {
		return nil, err
	}
	return &Staged{Dir: dstDir, Skills: skills, Renamed: renamed}, nil
}

// confine checks that dir resolves to a place inside base. It opens base as its
// own root, so a symlink that leaves base fails.
func confine(root *os.Root, base, dir string) error {
	br, err := root.OpenRoot(base)
	if err != nil {
		return err
	}
	defer br.Close()
	rel := strings.TrimPrefix(strings.TrimPrefix(dir, base), "/")
	if rel == "" {
		rel = "."
	}
	if _, err := br.Stat(rel); err != nil {
		return fmt.Errorf("the skills directory %q is not inside the fetched source: %w", rel, err)
	}
	return nil
}

// hasSkills reports whether dir (inside the root) has a skill subdirectory.
func hasSkills(root *os.Root, dir string) bool {
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(root.FS(), path.Join(dir, e.Name(), "SKILL.md")); err == nil {
			return true
		}
	}
	return false
}

var errNoSkills = errors.New("no skills found: no directory with a SKILL.md")
