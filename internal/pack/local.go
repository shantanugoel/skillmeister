package pack

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// fetchLocal copies the skill directories of a local directory. spec.Source is
// the directory; it may lie outside the library.
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
	skills, renamed, err := normalizeInto(srcFS, ".", func(orig, norm string) error {
		return copyTree(srcFS, orig, root, path.Join(dstDir, norm))
	})
	if err != nil {
		return nil, fmt.Errorf("local source %s: %w", src, err)
	}
	return &Staged{Dir: dstDir, Skills: skills, Renamed: renamed}, nil
}
