package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
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
	if spec.Fetch != manifest.FetchLocal {
		ignored, err := ignoredFiles(root, s.Dir)
		if err != nil {
			root.RemoveAll(work)
			return nil, err
		}
		if len(ignored) > 0 {
			s.Notes = append(s.Notes, fmt.Sprintf("the library .gitignore leaves out these fetched files: %s. "+
				"A clone of the library does not get them, and for a pinned pack doctor then reports modified", joinShort(ignored, 8)))
		}
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

// singleMove renames dir (inside the root and inside base) to work/pack/<name>
// as the one skill of the pack. The .git directory and the .gitattributes file
// in dir are removed first: the library repo must not hold a nested repo, and
// the attributes of the source must not override those of the library.
func singleMove(root *os.Root, work, base, dir, packName string) (*Staged, error) {
	if err := confine(root, base, dir); err != nil {
		return nil, err
	}
	// A rename moves a symlink and not its target, so the pack would hold a
	// link to a place that the fetch did not check.
	if info, err := root.Lstat(dir); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("the skill directory %q is a symlink or not a directory", strings.TrimPrefix(strings.TrimPrefix(dir, base), "/"))
	}
	name := singleName(root.FS(), dir, packName)
	for _, meta := range []string{".git", ".gitattributes"} {
		if err := root.RemoveAll(path.Join(dir, meta)); err != nil {
			return nil, err
		}
	}
	dstDir := path.Join(work, "pack")
	if err := root.Mkdir(dstDir, 0o755); err != nil {
		return nil, err
	}
	if err := root.Rename(dir, path.Join(dstDir, name)); err != nil {
		return nil, err
	}
	return &Staged{Dir: dstDir, Skills: []string{name}}, nil
}

// singleName picks the skill name for a single-skill source: the normalized
// frontmatter name when it is valid, else the pack name.
func singleName(src fs.FS, dir, packName string) string {
	data, err := fs.ReadFile(src, path.Join(dir, library.SkillFile))
	if err != nil || len(data) > library.MaxSkillFile {
		return packName
	}
	fm, _, _, err := library.ParseSkill(data)
	if err != nil {
		return packName
	}
	if n := library.NormalizeName(fm.Name); manifest.NameRE.MatchString(n) {
		return n
	}
	return packName
}

// ignoredFiles lists the staged paths that the library .gitignore leaves out:
// setup directories, bytecode, and dotenv files. Git does not track them, but
// the tree hash includes them.
func ignoredFiles(root *os.Root, dir string) ([]string, error) {
	var out []string
	err := fs.WalkDir(root.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		n := d.Name()
		switch {
		case p == dir:
		case d.IsDir() && setupNames[n]:
			out = append(out, strings.TrimPrefix(p, dir+"/")+"/")
			return fs.SkipDir
		case !d.IsDir() && (IsSecretEnv(n) || strings.HasSuffix(n, ".pyc")):
			out = append(out, strings.TrimPrefix(p, dir+"/"))
		}
		return nil
	})
	return out, err
}

// hasSkillFile reports whether dir itself holds a SKILL.md file.
func hasSkillFile(src fs.FS, dir string) bool {
	info, err := fs.Stat(src, path.Join(dir, library.SkillFile))
	return err == nil && info.Mode().IsRegular()
}

// countSkills counts the skill subdirectories of dir.
func countSkills(src fs.FS, dir string) int {
	entries, err := fs.ReadDir(src, dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && hasSkillFile(src, path.Join(dir, e.Name())) {
			n++
		}
	}
	return n
}

// hasSkills reports whether dir (inside the root) has a skill subdirectory.
func hasSkills(root *os.Root, dir string) bool { return countSkills(root.FS(), dir) > 0 }

var errNoSkills = errors.New("no skills found: no directory with a SKILL.md")
