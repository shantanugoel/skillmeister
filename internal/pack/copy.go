package pack

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// skip names are never copied from a source.
var skipNames = map[string]bool{".git": true, ".import": true, ".removed": true, ".DS_Store": true}

// setupNames are made by a local setup (a virtual environment, installed
// packages, bytecode). A local copy leaves them out and reports them.
var setupNames = map[string]bool{".venv": true, "node_modules": true, "__pycache__": true}

// IsSecretEnv reports whether a file name is a dotenv file that can hold
// secrets. The example, sample, and template forms are not.
func IsSecretEnv(name string) bool {
	if name == ".env" {
		return true
	}
	rest, ok := strings.CutPrefix(name, ".env.")
	return ok && rest != "example" && rest != "sample" && rest != "template"
}

// copyTree copies the directory src of srcFS to dst inside root. Files keep
// their permission bits. A symlink is copied only when its target is relative
// and stays inside the copied directory. It leaves out setup directories and
// dotenv files and returns their paths.
func copyTree(srcFS fs.FS, src string, root *os.Root, dst string) (skipped []string, err error) {
	return skipped, copyTreeIn(srcFS, src, src, root, dst, &skipped)
}

func copyTreeIn(srcFS fs.FS, top, src string, root *os.Root, dst string, skipped *[]string) error {
	if err := root.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(srcFS, src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if skipNames[e.Name()] {
			continue
		}
		if setupNames[e.Name()] || IsSecretEnv(e.Name()) {
			*skipped = append(*skipped, path.Join(src, e.Name()))
			continue
		}
		s := path.Join(src, e.Name())
		d := path.Join(dst, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			target, err := fs.ReadLink(srcFS, s)
			if err != nil {
				return err
			}
			if !linkStaysInside(top, s, target) {
				return fmt.Errorf("symlink %s -> %s leaves the skill directory", s, target)
			}
			if err := root.Symlink(target, d); err != nil {
				return err
			}
		case e.IsDir():
			if err := copyTreeIn(srcFS, top, s, root, d, skipped); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if err := copyFile(srcFS, s, root, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// linkStaysInside checks a symlink at link, whose target must resolve inside
// the directory top.
func linkStaysInside(top, link, target string) bool {
	if path.IsAbs(target) || strings.HasPrefix(target, "/") {
		return false
	}
	resolved := path.Join(path.Dir(link), target)
	if top == "." {
		return resolved != ".." && !strings.HasPrefix(resolved, "../")
	}
	return resolved == top || strings.HasPrefix(resolved, top+"/")
}

// joinShort joins up to max items and says how many more there are.
func joinShort(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:max], ", "), len(items)-max)
}

func copyFile(srcFS fs.FS, src string, root *os.Root, dst string) error {
	in, err := srcFS.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
