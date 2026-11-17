package cli

import (
	"fmt"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/doctor"
	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/pack"
)

func cmdSync(g *globals, args []string) error {
	fs := newFlagSet(g, "sync")
	frozen := fs.Bool("frozen", false, "verify only; write nothing")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("sync: unexpected argument %q", fs.Arg(0))
	}
	if *frozen {
		return syncFrozen(g)
	}
	return g.withWriter(func(lib *library.Library) error { return syncRepair(g, lib) })
}

// syncFrozen verifies the library and writes nothing. It exits 2 on drift.
func syncFrozen(g *globals) error {
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	rep, err := doctor.Run(lib)
	if err != nil {
		return ioErr(err)
	}
	bad := rep.IntegrityFindings()
	for _, f := range bad {
		g.printFinding(f)
	}
	if len(bad) > 0 {
		return &exitError{ExitDrift, fmt.Errorf("sync --frozen: %d problems", len(bad))}
	}
	g.printf("sync --frozen: clean\n")
	return nil
}

// syncRepair restores missing fetched packs at their locked commit, moves
// orphan pack directories to .removed, drops orphan lock entries, and deletes
// leftovers of a crash. It never overwrites a modified pinned pack.
func syncRepair(g *globals, lib *library.Library) error {
	rep, err := doctor.Run(lib)
	if err != nil {
		return ioErr(err)
	}
	lk, err := lock.Read(lib.Root)
	if err != nil {
		return ioErr(err)
	}
	m := lib.Manifest
	unresolved := 0
	lockChanged := false

	for _, f := range rep.Findings {
		switch f.Code {
		case doctor.CodeOrphanPack:
			dest, err := pack.QuarantineOrphan(lib.Root, f.Pack)
			if err != nil {
				return ioErr(err)
			}
			g.printf("moved orphan directory packs/%s to %s\n", f.Pack, dest)
		case doctor.CodeOrphanLock:
			delete(lk.Packs, f.Pack)
			lockChanged = true
			g.printf("dropped orphan lock entry %s\n", f.Pack)
		case doctor.CodeTombstone:
			if err := lib.Root.RemoveAll(f.Subject); err != nil {
				return ioErr(err)
			}
			g.printf("deleted leftover %s\n", f.Subject)
		case doctor.CodeLeftover:
			if err := lib.Root.RemoveAll(pack.ImportDir); err != nil {
				return ioErr(err)
			}
			g.printf("deleted leftover staging directory %s\n", pack.ImportDir)
		case doctor.CodeMissingPack:
			p := m.Packs[f.Pack]
			entry, locked := lk.Packs[f.Pack]
			if p == nil || p.Fetch == manifest.FetchLocal || !locked {
				fmt.Fprintf(g.env.Stderr, "skillmeister: %s: %s\n", f.Pack, f.Message)
				unresolved++
				continue
			}
			if err := restorePack(g, lib, p, entry); err != nil {
				return err
			}
		case doctor.CodeModified:
			fmt.Fprintf(g.env.Stderr, "skillmeister: %s: %s (sync does not overwrite it)\n", f.Pack, f.Message)
			unresolved++
		case doctor.CodeNoLockEntry, doctor.CodeLockMismatch:
			fmt.Fprintf(g.env.Stderr, "skillmeister: %s: %s\n", f.Pack, f.Message)
			unresolved++
		}
	}
	if lockChanged {
		if err := lk.Write(lib.Root); err != nil {
			return ioErr(err)
		}
	}
	if unresolved > 0 {
		return &exitError{ExitDrift, fmt.Errorf("sync: %d problems need your attention", unresolved)}
	}
	g.printf("sync: done\n")
	return nil
}

// restorePack re-fetches a missing pack at the commit in its lock entry and
// checks the result against the locked tree hash before it moves it into place.
func restorePack(g *globals, lib *library.Library, p *manifest.Pack, entry lock.Entry) error {
	spec := pack.Spec{Name: p.Name, Fetch: p.Fetch, Ref: entry.Resolved, Path: p.Path}
	switch p.Fetch {
	case manifest.FetchGit:
		spec.URL = p.URL
	case manifest.FetchNPX:
		spec.Source = p.Source
	}
	g.printf("restoring %s at %s...\n", p.Name, short12(entry.Resolved))
	staged, err := pack.Fetch(lib.Root, spec)
	if err != nil {
		return ioErr(fmt.Errorf("restore %s: %w (the locked commit may no longer exist)", p.Name, err))
	}
	defer pack.Cleanup(lib.Root, staged)
	got, err := lock.HashTree(lib.Root, staged.Dir)
	if err != nil {
		return ioErr(err)
	}
	if got != entry.Tree {
		return &exitError{ExitDrift, fmt.Errorf("restore %s: fetched content does not match the lock (lock %s, got %s); nothing was changed",
			p.Name, strings.TrimPrefix(entry.Tree, lock.HashPrefix)[:12], strings.TrimPrefix(got, lock.HashPrefix)[:12])}
	}
	if err := pack.Swap(lib.Root, staged, p.Name); err != nil {
		return ioErr(err)
	}
	g.printf("restored %s (%d skills)\n", p.Name, len(staged.Skills))
	return nil
}

func short12(s string) string { return s[:min(len(s), 12)] }
