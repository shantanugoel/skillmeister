package cli

import (
	"fmt"
	"path"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/pack"
)

func cmdUpdate(g *globals, args []string) error {
	fs := newFlagSet(g, "update")
	check := fs.Bool("check", false, "report newer upstream commits; write nothing")
	apply := fs.Bool("apply", false, "stage, swap, and move the lock entry")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *check && *apply {
		return usageErr("update: use --check or --apply, not both")
	}
	if fs.NArg() > 1 {
		return usageErr("update: at most one pack")
	}
	only := fs.Arg(0)
	if *apply {
		return g.withWriter(func(lib *library.Library) error { return updateApply(g, lib, only, *yes) })
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	return updateCheck(g, lib, only)
}

// fetchedPacks returns the npx and git packs to act on, in name order.
func fetchedPacks(lib *library.Library, only string) ([]*manifest.Pack, error) {
	m := lib.Manifest
	if only != "" {
		p := m.Packs[only]
		switch {
		case p == nil:
			return nil, usageErr("update: unknown pack %q", only)
		case p.Fetch == manifest.FetchLocal:
			return nil, usageErr("update: %q is a local pack; it has no upstream", only)
		}
		return []*manifest.Pack{p}, nil
	}
	var out []*manifest.Pack
	for _, n := range m.PackNames() {
		if p := m.Packs[n]; p.Fetch != manifest.FetchLocal {
			out = append(out, p)
		}
	}
	return out, nil
}

// updateCheck reads from the network but writes nothing in the library.
func updateCheck(g *globals, lib *library.Library, only string) error {
	packs, err := fetchedPacks(lib, only)
	if err != nil {
		return err
	}
	lk, err := lock.Read(lib.Root)
	if err != nil {
		return ioErr(err)
	}
	failed := 0
	for _, p := range packs {
		entry, locked := lk.Packs[p.Name]
		if !locked {
			g.printf("%-16s no lock entry; re-add the pack with `add --force`\n", p.Name)
			failed++
			continue
		}
		latest, err := pack.LatestCommit(p)
		switch {
		case err != nil:
			g.printf("%-16s cannot check: %s\n", p.Name, firstLine(err.Error()))
			failed++
		case latest.Pinned:
			g.printf("%-16s pinned to commit %s; `add --force --ref` moves it\n", p.Name, short12(latest.Commit))
		case latest.Commit == entry.Resolved:
			g.printf("%-16s up to date (%s)\n", p.Name, short12(entry.Resolved))
		default:
			g.printf("%-16s update available: %s -> %s\n", p.Name, short12(entry.Resolved), short12(latest.Commit))
		}
	}
	if failed > 0 {
		return &exitError{ExitIO, fmt.Errorf("update --check: %d packs could not be checked", failed)}
	}
	return nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func updateApply(g *globals, lib *library.Library, only string, yes bool) error {
	packs, err := fetchedPacks(lib, only)
	if err != nil {
		return err
	}
	failed, changed := 0, 0
	for _, p := range packs {
		did, err := applyOne(g, lib, p, yes)
		if err != nil {
			// A declined prompt or a usage problem stops the whole command.
			if ee, ok := err.(*exitError); ok && ee.code == ExitUsage {
				return err
			}
			fmt.Fprintf(g.env.Stderr, "skillmeister: update %s: %v\n", p.Name, err)
			failed++
			continue
		}
		if did {
			changed++
		}
	}
	if failed > 0 {
		return &exitError{ExitIO, fmt.Errorf("update --apply: %d packs failed", failed)}
	}
	if changed == 0 {
		g.printf("nothing to update\n")
	}
	return nil
}

// applyOne updates one pack. It reports whether the pack changed.
func applyOne(g *globals, lib *library.Library, p *manifest.Pack, yes bool) (bool, error) {
	lk, err := lock.Read(lib.Root)
	if err != nil {
		return false, ioErr(err)
	}
	entry, locked := lk.Packs[p.Name]
	if !locked {
		return false, fmt.Errorf("no lock entry; re-add the pack with `add --force`")
	}
	dir := path.Join(pack.PacksDir, p.Name)
	// A pinned pack whose files differ from the lock is modified. Update
	// restores the upstream content.
	modified := false
	if p.Trust == manifest.Pinned && pack.Exists(lib.Root, dir) {
		if got, err := lock.HashTree(lib.Root, dir); err == nil && got != entry.Tree {
			modified = true
		}
	}
	latest, err := pack.LatestCommit(p)
	if err != nil {
		return false, err
	}
	if latest.Pinned && !modified {
		g.printf("%s: pinned to commit %s; nothing to update\n", p.Name, short12(latest.Commit))
		return false, nil
	}
	if latest.Commit == entry.Resolved && !modified {
		g.printf("%s: up to date (%s)\n", p.Name, short12(entry.Resolved))
		return false, nil
	}
	spec := pack.Spec{Name: p.Name, Fetch: p.Fetch, Ref: p.Ref, Path: p.Path}
	if latest.Pinned {
		spec.Ref = entry.Resolved // restore the locked commit
	}
	switch p.Fetch {
	case manifest.FetchGit:
		spec.URL = p.URL
	case manifest.FetchNPX:
		spec.Source = p.Source
	}
	g.printf("fetching %s...\n", p.Name)
	staged, err := pack.Fetch(lib.Root, spec)
	if err != nil {
		return false, ioErr(err)
	}
	defer pack.Cleanup(lib.Root, staged)
	diff, err := pack.DiffTrees(lib.Root, dir, staged.Dir)
	if err != nil {
		return false, ioErr(err)
	}
	g.printDiff(p, entry.Resolved, staged.Resolved, diff, modified)
	if diff.Empty() && staged.Resolved == entry.Resolved {
		g.printf("%s: content is already current\n", p.Name)
		return false, nil
	}
	if err := g.confirm(fmt.Sprintf("Apply this update to %s?", p.Name), yes); err != nil {
		return false, err
	}
	tomb, err := pack.SwapKeep(lib.Root, staged, p.Name)
	if err != nil {
		return false, ioErr(err)
	}
	prev, err := lk.Marshal()
	if err != nil {
		return false, ioErr(err)
	}
	rollback := func(err error) (bool, error) {
		if rerr := pack.RestoreTombstone(lib.Root, tomb, p.Name); rerr != nil {
			err = fmt.Errorf("%w (and the old pack could not be restored from %s: %v)", err, tomb, rerr)
		}
		if werr := lock.WriteFileAtomic(lib.Root, lock.FileName, prev, 0o644); werr != nil {
			err = fmt.Errorf("%w (and the old lock could not be restored: %v)", err, werr)
		}
		return false, err
	}
	tree, err := lock.HashTree(lib.Root, dir)
	if err != nil {
		return rollback(ioErr(err))
	}
	lk.Packs[p.Name] = lock.Entry{Fetch: string(p.Fetch), Resolved: staged.Resolved, Tree: tree}
	if err := lk.Write(lib.Root); err != nil {
		return rollback(ioErr(err))
	}
	if err := pack.MergeSkillsLock(lib.Root, staged.LockEntries); err != nil {
		return rollback(ioErr(err))
	}
	if err := pack.DropTombstone(lib.Root, tomb); err != nil {
		return true, ioErr(err)
	}
	g.printf("updated %s to %s\n", p.Name, short12(staged.Resolved))
	return true, nil
}

func (g *globals) printDiff(p *manifest.Pack, from, to string, d *pack.Diff, modified bool) {
	g.printf("%s: %s -> %s\n", p.Name, short12(from), short12(to))
	if modified {
		g.printf("  warning: the pack has local edits. They will be overwritten by the upstream content.\n")
	}
	if len(d.AddedSkills) > 0 {
		g.printf("  new skills: %s\n", strings.Join(d.AddedSkills, ", "))
	}
	if len(d.RemovedSkills) > 0 {
		g.printf("  removed skills: %s\n", strings.Join(d.RemovedSkills, ", "))
	}
	g.printf("  files: %d added, %d changed, %d removed\n", len(d.Added), len(d.Changed), len(d.Removed))
	const show = 15
	for _, group := range []struct {
		tag   string
		files []string
	}{{"+", d.Added}, {"~", d.Changed}, {"-", d.Removed}} {
		for i, f := range group.files {
			if i >= show {
				g.printf("    %s ... and %d more\n", group.tag, len(group.files)-show)
				break
			}
			g.printf("    %s %s\n", group.tag, escape(f))
		}
	}
	if sc := d.Scripts(); len(sc) > 0 {
		g.printf("  scripts that changed (review them): %s\n", escape(strings.Join(sc, ", ")))
	}
}
