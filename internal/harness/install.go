package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// shimName is the front matter line that marks a skillmeister shim.
var shimName = []byte("\nname: skillmeister\n")

// checkTarget refuses to replace anything that skillmeister did not create. A
// missing path and an existing shim are fine.
func checkTarget(skillPath string) error {
	dir := filepath.Dir(skillPath)
	st, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; remove it first", dir)
	}
	data, err := os.ReadFile(skillPath)
	if st.IsDir() && errors.Is(err, fs.ErrNotExist) && onlyTempFiles(dir) {
		return nil // a failed earlier run left an empty directory
	}
	if !st.IsDir() || err != nil || !bytes.Contains(append([]byte("\n"), data...), shimName) {
		return fmt.Errorf("%s exists and is not a skillmeister shim; move it away first", dir)
	}
	return nil
}

// onlyTempFiles reports whether dir holds nothing but the temp files of an
// interrupted write.
func onlyTempFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".SKILL.md.") {
			return false
		}
	}
	return true
}

// CheckInstallable verifies that the shim can be installed. It writes nothing.
func CheckInstallable(home string) error {
	for _, p := range ShimPaths(home) {
		if err := checkTarget(p); err != nil {
			return err
		}
	}
	return nil
}

// InstallShim renders the shim into both discovery paths. It checks every
// target before it writes the first one, and it never replaces a directory
// that skillmeister did not create. It returns the files it wrote.
func InstallShim(home, bin, root string) ([]string, error) {
	if err := CheckInstallable(home); err != nil {
		return nil, err
	}
	out, err := Render(Template, bin, root)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, p := range ShimPaths(home) {
		if err := writeAtomic(p, out, 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	return written, nil
}

// InstallMemory appends the memory line to each memory file that lacks it. A
// file that does not end in a newline gets one first. It returns the files it
// changed.
func InstallMemory(home, root string) ([]string, error) {
	line := MemoryLine(root)
	var changed []string
	for _, p := range MemoryFiles(home) {
		data, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return changed, err
		}
		if containsAny(string(data), []string{line}) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return changed, err
		}
		add := line + "\n"
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			add = "\n" + add
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return changed, err
		}
		_, werr := f.WriteString(add)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return changed, werr
		}
		changed = append(changed, p)
	}
	return changed, nil
}

// StableBin returns a path for the binary that survives an upgrade. Homebrew
// puts the real file in a versioned Cellar directory and links it from
// <prefix>/bin. The link is the path to write into the shim.
func StableBin(exe string) string {
	i := strings.Index(exe, "/Cellar/")
	if i < 0 {
		return exe
	}
	link := filepath.Join(exe[:i], "bin", filepath.Base(exe))
	a, err1 := filepath.EvalSymlinks(link)
	b, err2 := filepath.EvalSymlinks(exe)
	if err1 == nil && err2 == nil && a == b {
		return link
	}
	return exe
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL.md.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
