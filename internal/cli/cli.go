// Package cli dispatches skillmeister subcommands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/version"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitUsage = 1 // usage or resolution error
	ExitDrift = 2 // drift or integrity failure
	ExitIO    = 3 // I/O, lock contention, or subprocess failure
)

// DefaultLibrary is the library location when no flag or variable names one.
const DefaultLibrary = "~/.skillmeister"

// Env carries the process environment so tests can run commands in-process.
type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	StdoutIsTTY bool
	StdinIsTTY  bool
}

// StdEnv returns the real process environment.
func StdEnv() Env {
	return Env{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      os.Getenv,
		StdoutIsTTY: isTTY(os.Stdout),
		StdinIsTTY:  isTTY(os.Stdin),
	}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// exitError carries an exit code to Run.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErr(format string, args ...any) error {
	return &exitError{ExitUsage, fmt.Errorf(format, args...)}
}

func ioErr(err error) error { return &exitError{ExitIO, err} }

// globals holds the global flags that precede the subcommand.
type globals struct {
	library string
	profile string
	noColor bool
	env     Env
}

// color reports whether human output may use ANSI codes.
func (g *globals) color() bool {
	return g.env.StdoutIsTTY && !g.noColor && g.env.Getenv("NO_COLOR") == ""
}

// libraryDir resolves the library root: --library, then SKILLMEISTER_ROOT,
// then the default location. A leading ~ is expanded.
func (g *globals) libraryDir() (string, error) {
	dir := g.library
	if dir == "" {
		dir = g.env.Getenv("SKILLMEISTER_ROOT")
	}
	if dir != "" {
		return g.expandHome(dir)
	}
	return g.expandHome(DefaultLibrary)
}

func hasManifest(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, manifest.FileName))
	return err == nil
}

func (g *globals) expandHome(dir string) (string, error) {
	if dir != "~" && !strings.HasPrefix(dir, "~/") {
		return dir, nil
	}
	home := g.env.Getenv("HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", usageErr("cannot expand ~ in %q: %v", dir, err)
		}
	}
	return filepath.Join(home, strings.TrimPrefix(dir, "~")), nil
}

// openLibrary resolves the library root and opens it.
func (g *globals) openLibrary() (*library.Library, error) {
	dir, err := g.libraryDir()
	if err != nil {
		return nil, err
	}
	lib, err := library.Open(dir)
	if err != nil {
		var me *manifest.Error
		if errors.Is(err, library.ErrNotLibrary) {
			return nil, usageErr("%v\n%s", err, g.noLibraryHint(dir))
		}
		if errors.As(err, &me) {
			return nil, usageErr("%v", err)
		}
		return nil, ioErr(err)
	}
	return lib, nil
}

// scope builds the scan scope from global and command flags.
func (g *globals) scope(packs []string, all bool) library.Scope {
	profile := g.profile
	if profile == "" {
		profile = g.env.Getenv("SKILLMEISTER_PROFILE")
	}
	return library.Scope{Profile: profile, Packs: packs, All: all}
}

// command is one subcommand.
type command struct {
	name string
	help string
	run  func(g *globals, args []string) error
}

func commands() []command {
	return []command{
		{"find", "find [--json] [--limit N] [--pack P] [--all] <query>", cmdFind},
		{"show", "show [--summary] [--raw] [--meta] <id|name>", cmdShow},
		{"path", "path <id>", cmdPath},
		{"list", "list [--json] [--pack P] [--all] [--trust T]", cmdList},
		{"add", "add [--as P] [--fetch local|npx|git] [--ref R] [--path D] [--trust T] [--force] <source>", cmdAdd},
		{"enable", "enable <pack|pack/skill>", cmdEnable},
		{"disable", "disable <pack|pack/skill>", cmdDisable},
		{"remove", "remove [--purge] <pack>", cmdRemove},
		{"update", "update [--check|--apply] [--yes] [pack]", cmdUpdate},
		{"sync", "sync [--frozen]", cmdSync},
		{"trust", "trust --level trusted|pinned [--accept] [--yes] <pack>", cmdTrust},
		{"doctor", "doctor [--security] [--json]", cmdDoctor},
		{"init", "init [--bin P] [--no-shim]   (create a library, install the shim)", cmdInit},
	}
}

// Run runs the CLI and returns the exit code.
func Run(args []string, env Env) int {
	g := &globals{env: env}
	fs := flag.NewFlagSet("skillmeister", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.library, "library", "", "library root")
	fs.StringVar(&g.profile, "profile", "", "profile name")
	fs.BoolVar(&g.noColor, "no-color", false, "disable color")
	showVersion := fs.Bool("version", false, "print the version")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(env.Stdout)
			return ExitOK
		}
		fmt.Fprintf(env.Stderr, "skillmeister: %v\n", err)
		printUsage(env.Stderr)
		return ExitUsage
	}
	if *showVersion {
		fmt.Fprintf(env.Stdout, "skillmeister %s\n", version.String())
		return ExitOK
	}
	rest := fs.Args()
	if len(rest) == 0 {
		printUsage(env.Stderr)
		if dir, err := g.libraryDir(); err == nil && !hasManifest(dir) {
			fmt.Fprintf(env.Stderr, "\nThere is no library yet. %s.\n", g.noLibraryHint(dir))
		}
		return ExitUsage
	}
	for _, c := range commands() {
		if c.name == rest[0] {
			return report(env, c.run(g, rest[1:]))
		}
	}
	fmt.Fprintf(env.Stderr, "skillmeister: unknown command %q\n", rest[0])
	printUsage(env.Stderr)
	return ExitUsage
}

func report(env Env, err error) int {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	var ee *exitError
	code := ExitIO
	if errors.As(err, &ee) {
		code = ee.code
	}
	fmt.Fprintf(env.Stderr, "skillmeister: %v\n", err)
	return code
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: skillmeister [--library PATH] [--profile P] [--no-color] <command> [flags] [args]")
	fmt.Fprintln(w, "       skillmeister --version")
	fmt.Fprintln(w, "commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %s\n", c.help)
	}
	fmt.Fprintln(w, "Flags go before positional arguments. Use -- to end flags.")
}

// newFlagSet builds a per-command flag set. Parse errors print to stderr.
func newFlagSet(g *globals, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(g.env.Stderr)
	return fs
}

// parse parses command flags and maps a parse failure to a usage error.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageErr("%s: invalid flags", fs.Name())
	}
	return nil
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// warn prints scan warnings to stderr.
func (g *globals) warn(ws []string) {
	for _, w := range ws {
		fmt.Fprintf(g.env.Stderr, "skillmeister: warning: %s\n", w)
	}
}
