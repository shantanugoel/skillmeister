package pack

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// fetchLocal copies the skills of a local directory. spec.Source is the
// directory; it may lie outside the library. Skill subdirectories win; else a
// SKILL.md in the directory itself makes the whole directory one skill.
func fetchLocal(root *os.Root, work string, spec Spec) (*Staged, error) {
	if spec.Ref != "" {
		return nil, fmt.Errorf("--ref is not valid for a local pack")
	}
	src, err := filepath.Abs(spec.Source)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("local source: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local source %s is not a directory", src)
	}
	srcFS := os.DirFS(src)
	dstDir := path.Join(work, "pack")
	if err := root.Mkdir(dstDir, 0o755); err != nil {
		return nil, err
	}
	var skipped []string
	copyOne := func(from, to string) error {
		s, err := copyTree(srcFS, from, root, path.Join(dstDir, to))
		skipped = append(skipped, s...)
		return err
	}
	var skills []string
	var renamed map[string]string
	if !hasSkillFile(srcFS, ".") || countSkills(srcFS, ".") > 0 {
		skills, renamed, err = normalizeInto(srcFS, ".", copyOne)
	} else {
		name := singleName(srcFS, ".", spec.Name)
		skills, err = []string{name}, copyOne(".", name)
	}
	if err != nil {
		return nil, fmt.Errorf("local source %s: %w", src, err)
	}
	s := &Staged{Dir: dstDir, Skills: skills, Renamed: renamed}
	if len(skipped) > 0 {
		s.Notes = append(s.Notes, fmt.Sprintf("not copied (local setup or secrets): %s", joinShort(skipped, 8)))
	}
	return s, nil
}
