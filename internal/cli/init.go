package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/harness"
	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

const initManifest = `version: 1

packs:
  # Your own skills live in skills/. Add other packs with ` + "`skillmeister add`" + `.
  own:
    fetch: local
    path: skills
    trust: trusted
    enabled: true

defaults:
  search_limit: 8
`

// libraryFiles are the files that init creates in a new library. Existing
// files are never overwritten.
var libraryFiles = []struct{ name, content string }{
	{manifest.FileName, initManifest},
	{"skills/.gitkeep", ""},
	{".gitignore", ".import/\n.removed/\n.skillmeister.lock\nnode_modules/\n"},
	{".gitattributes", "* -text\n"},
}

func cmdInit(g *globals, args []string) error {
	fs := newFlagSet(g, "init")
	bin := fs.String("bin", "", "absolute path of the binary to put in the shim (default: this binary)")
	noShim := fs.Bool("no-shim", false, "create the library only; do not touch agent config")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("init: unexpected argument %q", fs.Arg(0))
	}
	dir, err := g.libraryDir()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(dir) {
		if dir, err = filepath.Abs(dir); err != nil {
			return ioErr(err)
		}
	}
	// A library made through a symlinked path is recorded by its real path,
	// the same way install.sh records it.
	home := g.env.Getenv("HOME")
	if !*noShim && home == "" {
		return usageErr("init: HOME is not set; use --no-shim to skip the agent setup")
	}
	binPath := *bin
	if !*noShim && binPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return ioErr(err)
		}
		binPath = harness.StableBin(exe)
	}
	// Check every target and every path before anything is written.
	if !*noShim {
		if err := harness.CheckInstallable(home); err != nil {
			return usageErr("init: %v", err)
		}
		real := dir
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			real = r
		}
		if _, err := harness.Render(harness.Template, binPath, real); err != nil {
			return usageErr("init: %v", err)
		}
	}
	created, err := ensureLibrary(dir)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	lib, err := library.Open(dir)
	if err != nil {
		return usageErr("init: %v", err)
	}
	defer lib.Close()
	if created {
		g.printf("created the library at %s\n", dir)
	} else {
		g.printf("the library at %s already exists; no library file was changed\n", dir)
	}
	if *noShim {
		g.printf("skipped the agent setup (--no-shim)\n")
	} else {
		shims, err := harness.InstallShim(home, binPath, lib.Abs)
		for _, p := range shims {
			g.printf("wrote the shim %s\n", p)
		}
		if err != nil {
			return ioErr(err)
		}
		changed, err := harness.InstallMemory(home, lib.Abs)
		if err != nil {
			return ioErr(err)
		}
		for _, p := range changed {
			g.printf("added the memory line to %s\n", p)
		}
		if len(changed) == 0 {
			g.printf("the memory lines are already in place\n")
		}
	}
	if created {
		g.printf("\nnext:\n")
		g.printf("  put your own skills in %s\n", filepath.Join(dir, "skills"))
		g.printf("  or add a pack: skillmeister add <source>\n")
		g.printf("  keep the library in git: cd %s && git init && git add -A\n", dir)
	}
	return nil
}

// ensureLibrary creates the library files in dir. It refuses a directory that
// holds other files but no manifest. It reports whether it created anything.
func ensureLibrary(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, ioErr(err)
	}
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, ioErr(err)
	case len(entries) > 0:
		return false, usageErr("init: %s is not empty and has no %s; pick an empty directory with --library", dir, manifest.FileName)
	}
	for _, f := range libraryFiles {
		full := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return false, ioErr(err)
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			return false, ioErr(err)
		}
	}
	return true, nil
}

// noLibraryHint tells the user how to make a library. It says so only when
// init can make one there: the directory is missing or empty.
func (g *globals) noLibraryHint(dir string) string {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Sprintf("%s has files but no %s; pick another place with --library", shortHome(g, dir), manifest.FileName)
	}
	if g.library == "" && g.env.Getenv("SKILLMEISTER_ROOT") == "" {
		return "run `skillmeister init` to create a library at " + shortHome(g, dir)
	}
	return fmt.Sprintf("run `skillmeister --library %s init` to create it", shellQuote(dir))
}

// shellQuote quotes a path for a shell when it needs it.
func shellQuote(p string) string {
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-~+@%") == "" {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func shortHome(g *globals, p string) string {
	if home := g.env.Getenv("HOME"); home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
