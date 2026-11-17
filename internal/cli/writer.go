package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// withWriter opens the library, takes the exclusive writer lock, reloads the
// manifest under the lock, and runs fn. A second writer exits 3.
func (g *globals) withWriter(fn func(lib *library.Library) error) error {
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	w, err := lock.Acquire(lib.Root)
	if errors.Is(err, lock.ErrBusy) {
		return &exitError{ExitIO, err}
	}
	if err != nil {
		return ioErr(err)
	}
	defer w.Release()
	// Another writer may have changed the manifest before we got the lock.
	m, err := manifest.Load(lib.Root)
	if err != nil {
		return usageErr("%v", err)
	}
	lib.Manifest = m
	return fn(lib)
}

// confirm asks a yes or no question. It prompts only when stdin is a terminal;
// otherwise --yes is required. Declining exits 1.
func (g *globals) confirm(question string, yes bool) error {
	if yes {
		return nil
	}
	if !g.env.StdinIsTTY {
		return usageErr("%s: confirmation is needed; pass --yes to proceed without a prompt", question)
	}
	fmt.Fprintf(g.env.Stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(g.env.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return usageErr("declined")
}

func (g *globals) printf(format string, args ...any) {
	fmt.Fprintf(g.env.Stdout, format, args...)
}
