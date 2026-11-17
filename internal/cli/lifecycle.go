package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/pack"
)

func cmdEnable(g *globals, args []string) error  { return enableDisable(g, "enable", true, args) }
func cmdDisable(g *globals, args []string) error { return enableDisable(g, "disable", false, args) }

// enableDisable changes the manifest only. It never touches pack files.
func enableDisable(g *globals, verb string, enable bool, args []string) error {
	fs := newFlagSet(g, verb)
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("%s: exactly one pack or pack/skill is required", verb)
	}
	target := fs.Arg(0)
	return g.withWriter(func(lib *library.Library) error {
		doc, err := manifest.OpenDoc(lib.Root)
		if err != nil {
			return usageErr("%v", err)
		}
		packName, skill, isSkill := strings.Cut(target, "/")
		p := lib.Manifest.Packs[packName]
		if p == nil {
			return usageErr("%s: unknown pack %q (known: %s)", verb, packName, strings.Join(lib.Manifest.PackNames(), ", "))
		}
		if !isSkill {
			if p.IsEnabled() == enable {
				g.printf("pack %s is already %sd\n", packName, verb)
				return nil
			}
			if err := doc.SetEnabled(packName, enable); err != nil {
				return ioErr(err)
			}
			if err := doc.Save(lib.Root); err != nil {
				return ioErr(err)
			}
			g.printf("%sd pack %s\n", verb, packName)
			return nil
		}
		skill = library.NormalizeName(skill)
		if !manifest.NameRE.MatchString(skill) {
			return usageErr("%s: %q is not a valid skill name", verb, skill)
		}
		list := slices.Clone(p.Disable)
		has := slices.Contains(list, skill)
		if enable == !has {
			g.printf("skill %s/%s is already %sd\n", packName, skill, verb)
			return nil
		}
		if enable {
			list = slices.DeleteFunc(list, func(s string) bool { return s == skill })
		} else {
			// Only a skill that exists can be disabled; a typo would pass silently.
			if _, _, err := library.Resolve(mustScanPack(lib, p), packName+"/"+skill); err != nil {
				return usageErr("%s: pack %s has no skill %q", verb, packName, skill)
			}
			list = append(list, skill)
			slices.Sort(list)
		}
		if err := doc.SetDisable(packName, list); err != nil {
			return ioErr(err)
		}
		if err := doc.Save(lib.Root); err != nil {
			return ioErr(err)
		}
		g.printf("%sd skill %s/%s\n", verb, packName, skill)
		if enable && !p.IsEnabled() {
			g.printf("note: pack %s is disabled; `skillmeister enable %s` turns it on\n", packName, packName)
		}
		return nil
	})
}

func mustScanPack(lib *library.Library, p *manifest.Pack) []*library.Record {
	recs, _ := lib.ScanPack(p, true)
	return recs
}

func cmdTrust(g *globals, args []string) error {
	fs := newFlagSet(g, "trust")
	level := fs.String("level", "", "trusted or pinned")
	accept := fs.Bool("accept", false, "re-pin the current content")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("trust: exactly one pack is required")
	}
	lv := manifest.Trust(*level)
	if lv != manifest.Trusted && lv != manifest.Pinned {
		return usageErr("trust: --level must be trusted or pinned")
	}
	name := fs.Arg(0)
	return g.withWriter(func(lib *library.Library) error {
		p := lib.Manifest.Packs[name]
		if p == nil {
			return usageErr("trust: unknown pack %q", name)
		}
		doc, err := manifest.OpenDoc(lib.Root)
		if err != nil {
			return usageErr("%v", err)
		}
		switch {
		case lv == manifest.Pinned && p.Fetch == manifest.FetchLocal:
			return usageErr("trust: pinned is not valid for the local pack %q (a local pack has no lock entry)", name)
		case lv == manifest.Trusted:
			if p.Trust == manifest.Trusted {
				g.printf("pack %s is already trusted\n", name)
				return nil
			}
			if p.Fetch != manifest.FetchLocal {
				if err := g.confirm(fmt.Sprintf("Trust the fetched pack %s? This turns off drift detection for it.", name), *yes); err != nil {
					return err
				}
			}
		case lv == manifest.Pinned:
			if err := g.repin(lib, p, *accept); err != nil {
				return err
			}
			if p.Trust == manifest.Pinned && !*accept {
				// Already pinned and the hash matches: nothing else to write.
				g.printf("pack %s is already pinned and verified\n", name)
				return nil
			}
		}
		if err := doc.SetTrust(name, lv); err != nil {
			return ioErr(err)
		}
		if err := doc.Save(lib.Root); err != nil {
			return ioErr(err)
		}
		g.printf("pack %s is now %s\n", name, lv)
		return nil
	})
}

// repin checks the tree hash of a fetched pack against its lock entry. A
// mismatch is an integrity failure unless accept is set, which rewrites the
// lock to the current content.
func (g *globals) repin(lib *library.Library, p *manifest.Pack, accept bool) error {
	lk, err := lock.Read(lib.Root)
	if err != nil {
		return ioErr(err)
	}
	entry, ok := lk.Packs[p.Name]
	if !ok {
		return usageErr("trust: pack %q has no lock entry; re-add it with `add --force`", p.Name)
	}
	got, err := lock.HashTree(lib.Root, p.Dir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &exitError{ExitDrift, fmt.Errorf("pack %q is missing; run `skillmeister sync`", p.Name)}
		}
		return ioErr(err)
	}
	if got == entry.Tree {
		return nil
	}
	if !accept {
		return &exitError{ExitDrift, fmt.Errorf("pack %q differs from its lock entry; review the change, then use --accept to pin the current content", p.Name)}
	}
	entry.Tree = got
	lk.Packs[p.Name] = entry
	if err := lk.Write(lib.Root); err != nil {
		return ioErr(err)
	}
	g.printf("lock entry for %s now holds the current content\n", p.Name)
	return nil
}

func cmdRemove(g *globals, args []string) error {
	fs := newFlagSet(g, "remove")
	purge := fs.Bool("purge", false, "delete the pack files instead of quarantining them")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("remove: exactly one pack is required")
	}
	name := fs.Arg(0)
	return g.withWriter(func(lib *library.Library) error {
		p := lib.Manifest.Packs[name]
		switch {
		case p == nil:
			return usageErr("remove: unknown pack %q", name)
		case name == manifest.OwnPack:
			return usageErr("remove: the own pack holds your skills and cannot be removed")
		}
		doc, err := manifest.OpenDoc(lib.Root)
		if err != nil {
			return usageErr("%v", err)
		}
		profiles, err := doc.RemovePack(name)
		if err != nil {
			return ioErr(err)
		}
		lk, err := lock.Read(lib.Root)
		if err != nil {
			return ioErr(err)
		}
		// Order: manifest, lock, files. A crash leaves an orphan that doctor
		// reports and sync repairs.
		if err := doc.Save(lib.Root); err != nil {
			return ioErr(err)
		}
		if _, ok := lk.Packs[name]; ok {
			delete(lk.Packs, name)
			if err := lk.Write(lib.Root); err != nil {
				return ioErr(err)
			}
		}
		if p.Fetch == manifest.FetchNPX {
			if err := pack.DropSkillsLock(lib.Root, p.Source); err != nil {
				return ioErr(err)
			}
		}
		dir := "packs/" + name
		switch {
		case !pack.Exists(lib.Root, dir):
			g.printf("removed pack %s (its directory was already missing)\n", name)
		case *purge:
			if err := lib.Root.RemoveAll(dir); err != nil {
				return ioErr(err)
			}
			g.printf("removed pack %s and deleted %s\n", name, dir)
		default:
			dest, err := pack.Quarantine(lib.Root, name)
			if err != nil {
				return ioErr(err)
			}
			g.printf("removed pack %s; files moved to %s\n", name, dest)
		}
		if len(profiles) > 0 {
			g.printf("dropped %s from profiles: %s\n", name, strings.Join(profiles, ", "))
		}
		return nil
	})
}
