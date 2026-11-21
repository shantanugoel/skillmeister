package cli

import (
	"fmt"
	"net/url"
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

// inferFetch picks the fetch mechanism from the source text. A path is local,
// a URL is git, and other text (owner/repo) goes to the `skills` CLI.
func inferFetch(source string) manifest.Fetch {
	lower := strings.ToLower(source)
	switch {
	case strings.HasPrefix(source, "/"), strings.HasPrefix(source, "./"), strings.HasPrefix(source, "../"),
		strings.HasPrefix(source, "~"), source == ".", source == "..":
		return manifest.FetchLocal
	case strings.HasPrefix(source, "git@"), strings.HasPrefix(lower, "ssh://"),
		strings.HasPrefix(lower, "file://"), strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "http://"), strings.HasSuffix(source, ".git"):
		return manifest.FetchGit
	}
	if st, err := os.Stat(source); err == nil && st.IsDir() {
		return manifest.FetchLocal
	}
	return manifest.FetchNPX
}

// isNPMPage reports whether source is a package page on npmjs.com.
func isNPMPage(source string) bool {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "npmjs.com", "www.npmjs.com", "npmjs.org", "www.npmjs.org", "npm.im":
		return true
	}
	return false
}

// hostedURL is a github.com or gitlab.com URL that add split into its parts.
type hostedURL struct {
	repo string // the clone URL, with no query, fragment, or page path
	ref  string // the ref from a directory URL, or empty
	sub  string // the subtree from a directory URL, or empty
}

// splitHostedURL reads a github.com or gitlab.com URL that a browser shows: a
// repo page (https://github.com/o/r?tab=readme-ov-file) or a directory page
// (https://github.com/o/r/tree/main/skills). The first segment after tree/ is
// the ref, so a branch name with a slash is not supported. It returns nil for
// a URL of a different host, and an error for a page that is not a repo or a
// directory, such as a file (blob) page.
func splitHostedURL(source string) (*hostedURL, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return nil, nil
	}
	host := strings.ToLower(u.Hostname())
	var segs []string
	for _, s := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		d, err := url.PathUnescape(s)
		if err != nil {
			return nil, nil
		}
		segs = append(segs, d)
	}
	// repoEnd is the number of segments that name the repo. page is the rest.
	var repoEnd int
	var page []string
	switch host {
	case "github.com", "www.github.com":
		if len(segs) < 2 {
			return nil, nil
		}
		repoEnd, page = 2, segs[2:]
	case "gitlab.com", "www.gitlab.com":
		repoEnd = len(segs)
		for i, s := range segs {
			if s == "-" {
				repoEnd = i
				break
			}
		}
		if repoEnd < 2 {
			return nil, nil
		}
		page = segs[repoEnd:]
		if len(page) > 0 {
			page = page[1:] // drop the "-"
			if len(page) == 0 {
				return nil, fmt.Errorf("%s is not a repo or directory URL", source)
			}
		}
	default:
		return nil, nil
	}
	h := &hostedURL{}
	switch {
	case len(page) == 0:
	case page[0] == "tree" && len(page) >= 2 && page[1] != "":
		h.ref = page[1]
		h.sub = path.Join(page[2:]...)
	default:
		return nil, fmt.Errorf("%s is not a repo or directory URL; copy the URL of the repo or of a directory (tree), not of a file", source)
	}
	u.Path = "/" + strings.Join(segs[:repoEnd], "/")
	u.RawPath, u.RawQuery, u.Fragment, u.ForceQuery = "", "", "", false
	h.repo = u.String()
	return h, nil
}

// genericDirs are subtree names that say nothing about the pack, so the pack
// ID comes from the repo instead.
var genericDirs = map[string]bool{"skills": true, ".skills": true, "agents": true, "own": true}

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
	subPath := fs.String("path", "", "subtree of a git repo that holds the skills, or one skill")
	force := fs.Bool("force", false, "replace an existing pack")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("add: exactly one source is required")
	}
	source := fs.Arg(0)

	fetch := manifest.Fetch(*fetchFlag)
	inferred := fetch == ""
	switch fetch {
	case "":
		if isNPMPage(source) {
			return usageErr("add: %s is an npm package page, not a git repo. "+
				"Use the repo URL of the package (the Repository link on that page)", source)
		}
		fetch = inferFetch(source)
	case manifest.FetchLocal, manifest.FetchNPX, manifest.FetchGit:
	default:
		return usageErr("add: --fetch must be local, npx, or git")
	}
	// A github.com or gitlab.com page URL gives the clone URL, and a directory
	// URL also gives the ref and the subtree. Explicit --ref and --path win
	// over the URL. The pack ID comes from the subtree, unless the subtree ends
	// in a generic name such as skills.
	idSource := source
	if fetch == manifest.FetchGit {
		h, err := splitHostedURL(source)
		if err != nil {
			return usageErr("add: %v", err)
		}
		if h != nil {
			source, idSource = h.repo, h.repo
			if *ref == "" {
				*ref = h.ref
			}
			if *subPath == "" && h.sub != "" {
				*subPath = h.sub
				if id, err := derivePackID(h.sub); err == nil && !genericDirs[path.Base(h.sub)] {
					idSource = id
				}
			}
		}
	}
	if fetch == manifest.FetchLocal && *ref != "" {
		return usageErr("add: --ref is not valid for a local pack")
	}
	if fetch == manifest.FetchNPX && *ref != "" {
		if r := pack.NPXSourceRef(source); r != "" {
			return usageErr("add: %s already names the ref %q; remove it from the source, or do not pass --ref", source, r)
		}
	}
	if *subPath != "" && fetch == manifest.FetchNPX {
		return usageErr("add: --path is only valid for local and git sources")
	}
	name := *as
	if name == "" {
		id, err := derivePackID(idSource)
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
		clearRef := false
		spec := pack.Spec{Name: name, Fetch: fetch, Ref: *ref, Path: *subPath, Log: g.env.Stderr}
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
				if inferred {
					return usageErr("add: pack %q uses fetch %s, but %s looks like fetch %s; pass --fetch %s to keep the method, or remove the pack first to change it",
						name, existing.Fetch, source, fetch, existing.Fetch)
				}
				return usageErr("add: pack %q uses fetch %s; remove it first to change the fetch method", name, existing.Fetch)
			}
			// A source that names its ref replaces the old ref. Else the
			// old ref stays.
			sourceRef := fetch == manifest.FetchNPX && pack.NPXSourceRef(source) != ""
			if spec.Ref == "" && !sourceRef {
				spec.Ref = existing.Ref
			}
			if sourceRef && existing.Ref != "" {
				clearRef = true
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
			if inferred && fetch == manifest.FetchGit {
				err = fmt.Errorf("%w (if %s is not a git repo, pass --fetch npx)", err, source)
			}
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
			if err == nil && clearRef {
				err = doc.ClearField(name, "ref")
			}
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
	for _, n := range s.Notes {
		g.printf("  note: %s\n", n)
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
