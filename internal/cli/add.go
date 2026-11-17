package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/pack"
)

// inferFetch picks the fetch mechanism from the source text.
func inferFetch(source string) manifest.Fetch {
	switch {
	case strings.HasPrefix(source, "/"), strings.HasPrefix(source, "./"), strings.HasPrefix(source, "../"),
		strings.HasPrefix(source, "~"), source == ".", source == "..":
		return manifest.FetchLocal
	case strings.HasPrefix(source, "git@"), strings.HasPrefix(source, "ssh://"),
		strings.HasPrefix(source, "file://"), strings.HasSuffix(source, ".git"):
		return manifest.FetchGit
	}
	if st, err := os.Stat(source); err == nil && st.IsDir() {
		return manifest.FetchLocal
	}
	return manifest.FetchNPX
}

// derivePackID builds a pack ID from the last path segment of the source.
func derivePackID(source string) (string, error) {
	s := strings.TrimRight(source, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	if i := strings.IndexAny(s, "#@"); i > 0 {
		s = s[:i]
	}
	id := library.NormalizeName(s)
	if err := manifest.ValidPackID(id); err != nil {
		return "", fmt.Errorf("cannot derive a pack ID from %q (%v); pass --as", source, err)
	}
	return id, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func cmdAdd(g *globals, args []string) error {
	fs := newFlagSet(g, "add")
	as := fs.String("as", "", "pack ID")
	fetchFlag := fs.String("fetch", "", "local, npx, or git")
	ref := fs.String("ref", "", "branch, tag, or commit")
	trustFlag := fs.String("trust", "", "trusted, pinned, or untrusted")
	subPath := fs.String("path", "", "subtree of a git repo that holds the skills")
	force := fs.Bool("force", false, "replace an existing pack")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("add: exactly one source is required")
	}
	source := fs.Arg(0)

	fetch := manifest.Fetch(*fetchFlag)
	switch fetch {
	case "":
		fetch = inferFetch(source)
	case manifest.FetchLocal, manifest.FetchNPX, manifest.FetchGit:
	default:
		return usageErr("add: --fetch must be local, npx, or git")
	}
	if fetch == manifest.FetchLocal && *ref != "" {
		return usageErr("add: --ref is not valid for a local pack")
	}
	if *subPath != "" && fetch == manifest.FetchNPX {
		return usageErr("add: --path is only valid for local and git sources")
	}
	name := *as
	if name == "" {
		id, err := derivePackID(source)
		if err != nil {
			return usageErr("add: %v", err)
		}
		name = id
	}
	if err := manifest.ValidPackID(name); err != nil {
		return usageErr("add: %v", err)
	}
	var trust manifest.Trust
	switch t := manifest.Trust(*trustFlag); t {
	case "", manifest.Trusted, manifest.Untrusted:
		trust = t
	case manifest.Pinned:
		if fetch == manifest.FetchLocal {
			return usageErr("add: pinned is not valid for a local pack")
		}
		trust = t
	default:
		return usageErr("add: --trust must be trusted, pinned, or untrusted")
	}

	return g.withWriter(func(lib *library.Library) error {
		existing := lib.Manifest.Packs[name]
		spec := pack.Spec{Name: name, Fetch: fetch, Ref: *ref, Path: *subPath}
		switch fetch {
		case manifest.FetchLocal:
			spec.Source = expandHome(source)
		case manifest.FetchGit:
			spec.URL = source
		case manifest.FetchNPX:
			spec.Source = source
		}
		if existing != nil {
			if !*force {
				return usageErr("add: pack %q already exists; pass --force to replace it", name)
			}
			if existing.Fetch != fetch {
				return usageErr("add: pack %q uses fetch %s; remove it first to change the fetch method", name, existing.Fetch)
			}
			if spec.Ref == "" {
				spec.Ref = existing.Ref
			}
			if spec.Path == "" && fetch == manifest.FetchGit {
				spec.Path = existing.Path
			}
		}
		if existing == nil && pack.Exists(lib.Root, path.Join(pack.PacksDir, name)) {
			return usageErr("add: directory packs/%s exists but is not in the manifest; run `skillmeister sync` first", name)
		}

		g.printf("fetching %s (%s)...\n", source, fetch)
		staged, err := pack.Fetch(lib.Root, spec)
		if err != nil {
			return ioErr(fmt.Errorf("add %s: %w", name, err))
		}
		defer pack.Cleanup(lib.Root, staged)
		if staged.Size > pack.LargePackBytes {
			fmt.Fprintf(g.env.Stderr, "skillmeister: warning: pack %s is %.1f MB; keep large binary assets in their own pack\n", name, float64(staged.Size)/(1<<20))
		}

		tomb, err := pack.SwapKeep(lib.Root, staged, name)
		if err != nil {
			return ioErr(fmt.Errorf("add %s: move into place: %w", name, err))
		}
		// A forced replace keeps the old pack until the lock and manifest are
		// written. On failure, the old pack and the old lock come back. A new
		// pack that fails here stays as an orphan that doctor reports and sync
		// repairs.
		prevLock, err := lock.Read(lib.Root)
		if err != nil {
			return ioErr(err)
		}
		prevBytes, err := prevLock.Marshal()
		if err != nil {
			return ioErr(err)
		}
		fail := func(err error) error {
			if tomb != "" {
				if rerr := pack.RestoreTombstone(lib.Root, tomb, name); rerr != nil {
					err = fmt.Errorf("%w (and the old pack could not be restored from %s: %v)", err, tomb, rerr)
				}
				if werr := lock.WriteFileAtomic(lib.Root, lock.FileName, prevBytes, 0o644); werr != nil {
					err = fmt.Errorf("%w (and the old lock could not be restored: %v)", err, werr)
				}
			}
			return err
		}
		// Order: files, lock, manifest.
		if fetch != manifest.FetchLocal {
			tree, err := lock.HashTree(lib.Root, path.Join(pack.PacksDir, name))
			if err != nil {
				return fail(ioErr(err))
			}
			prevLock.Packs[name] = lock.Entry{Fetch: string(fetch), Resolved: staged.Resolved, Tree: tree}
			if err := prevLock.Write(lib.Root); err != nil {
				return fail(ioErr(err))
			}
			if err := pack.MergeSkillsLock(lib.Root, staged.LockEntries); err != nil {
				return fail(ioErr(err))
			}
		}
		doc, err := manifest.OpenDoc(lib.Root)
		if err != nil {
			return fail(usageErr("%v", err))
		}
		pathField := staged.Path
		if fetch == manifest.FetchLocal {
			pathField = source
		}
		if existing == nil {
			np := &manifest.Pack{Fetch: fetch, Ref: spec.Ref, Path: pathField, Trust: trust}
			if np.Trust == "" {
				np.Trust = manifest.Untrusted
			}
			switch fetch {
			case manifest.FetchGit:
				np.URL = source
			case manifest.FetchNPX:
				np.Source = source
			}
			err = doc.AddPack(name, np)
		} else {
			err = updateExisting(doc, existing, spec, pathField, source, trust)
		}
		if err != nil {
			return fail(ioErr(err))
		}
		if err := doc.Save(lib.Root); err != nil {
			return fail(ioErr(err))
		}
		if err := pack.DropTombstone(lib.Root, tomb); err != nil {
			return ioErr(err)
		}
		g.summarizeAdd(lib, name, fetch, staged, trust, existing)
		return nil
	})
}

// updateExisting writes the changed fields of a forced re-add.
func updateExisting(doc *manifest.Doc, old *manifest.Pack, spec pack.Spec, pathField, source string, trust manifest.Trust) error {
	set := func(field, now, was string) error {
		if now != "" && now != was {
			return doc.SetField(old.Name, field, now)
		}
		return nil
	}
	if err := set("ref", spec.Ref, old.Ref); err != nil {
		return err
	}
	if err := set("path", pathField, old.Path); err != nil {
		return err
	}
	switch old.Fetch {
	case manifest.FetchGit:
		if err := set("url", source, old.URL); err != nil {
			return err
		}
	case manifest.FetchNPX:
		if err := set("source", source, old.Source); err != nil {
			return err
		}
	}
	if trust != "" && trust != old.Trust {
		return doc.SetTrust(old.Name, trust)
	}
	return nil
}

func (g *globals) summarizeAdd(lib *library.Library, name string, fetch manifest.Fetch, s *pack.Staged, trust manifest.Trust, replaced *manifest.Pack) {
	if trust == "" {
		trust = manifest.Untrusted
		if replaced != nil {
			trust = replaced.Trust
		}
	}
	verb := "added"
	if replaced != nil {
		verb = "replaced"
	}
	resolved := ""
	if s.Resolved != "" {
		resolved = ", resolved " + s.Resolved[:min(len(s.Resolved), 12)]
	}
	g.printf("%s pack %s (%s, %d skills, trust %s%s)\n", verb, name, fetch, len(s.Skills), trust, resolved)
	g.printf("  skills: %s\n", strings.Join(s.Skills, ", "))
	if len(s.Renamed) > 0 {
		var rn []string
		for from, to := range s.Renamed {
			rn = append(rn, from+" -> "+to)
		}
		sort.Strings(rn)
		g.printf("  renamed: %s\n", strings.Join(rn, ", "))
	}
	p := &manifest.Pack{Name: name, Fetch: fetch, Trust: trust}
	recs, _ := lib.ScanPack(p, true)
	for _, r := range recs {
		if len(r.Warnings) > 0 {
			g.printf("  needs native semantics: %s (%s)\n", r.Name, strings.Join(r.Warnings, "; "))
		}
	}
	g.printf("review the diff with `git status`, then commit it. skillmeister does not commit.\n")
}
