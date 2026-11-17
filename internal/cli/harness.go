package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shantanugoel/skillmeister/internal/harness"
)

// cmdRenderShim prints the rendered shim, or the memory line. scripts/install.sh
// uses it, so one renderer produces the file that doctor checks.
func cmdRenderShim(g *globals, args []string) error {
	fs := newFlagSet(g, "render-shim")
	bin := fs.String("bin", "", "absolute path of the binary (default: this binary)")
	memory := fs.Bool("memory", false, "print the memory line instead of the shim")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("render-shim: unexpected argument %q", fs.Arg(0))
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	if *memory {
		fmt.Fprintln(g.env.Stdout, harness.MemoryLine(lib.Abs))
		return nil
	}
	b := *bin
	if b == "" {
		exe, err := os.Executable()
		if err != nil {
			return ioErr(err)
		}
		if b, err = filepath.EvalSymlinks(exe); err != nil {
			return ioErr(err)
		}
	}
	tmpl, err := lib.Root.ReadFile(harness.TemplatePath)
	if err != nil {
		return ioErr(fmt.Errorf("read shim template: %w", err))
	}
	out, err := harness.Render(tmpl, b, lib.Abs)
	if err != nil {
		return usageErr("render-shim: %v", err)
	}
	_, err = g.env.Stdout.Write(out)
	if err != nil {
		return ioErr(err)
	}
	return nil
}
