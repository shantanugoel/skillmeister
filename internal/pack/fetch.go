package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

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
func normalizeMove(root *os.Root, work, srcDir string) (*Staged, error) {
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
