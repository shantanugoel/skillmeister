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

// copyTree copies the directory src of srcFS to dst inside root. Files keep
// their permission bits. A symlink is copied only when its target is relative
// and stays inside the copied directory.
func copyTree(srcFS fs.FS, src string, root *os.Root, dst string) error {
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
		s := path.Join(src, e.Name())
		d := path.Join(dst, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			target, err := fs.ReadLink(srcFS, s)
			if err != nil {
				return err
			}
			if !linkStaysInside(src, s, target) {
				return fmt.Errorf("symlink %s -> %s leaves the skill directory", s, target)
			}
			if err := root.Symlink(target, d); err != nil {
				return err
			}
		case e.IsDir():
			if err := copyTree(srcFS, s, root, d); err != nil {
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
	return resolved == top || strings.HasPrefix(resolved, top+"/")
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
