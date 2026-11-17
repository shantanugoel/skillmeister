package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/search"
)

// recordJSON is the stable record shape shared by `find --json` and
// `list --json`. `list` omits score.
type recordJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Pack        string   `json:"pack"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Trust       string   `json:"trust"`
	Score       int      `json:"score,omitempty"`
	Warnings    []string `json:"warnings"`
}

func toJSON(r *library.Record, score int) recordJSON {
	w := r.Warnings
	if w == nil {
		w = []string{}
	}
	return recordJSON{
		ID: r.ID, Name: r.Name, Pack: r.Pack, Description: r.Description,
		Path: r.Path, Trust: string(r.Trust), Score: score, Warnings: w,
	}
}

// writeJSONLines prints one JSON object per line, without HTML escaping.
func writeJSONLines(w io.Writer, recs []recordJSON) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return ioErr(err)
		}
	}
	return nil
}

func (g *globals) bold(s string) string {
	if g.color() {
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return s
}

func (g *globals) dim(s string) string {
	if g.color() {
		return "\x1b[2m" + s + "\x1b[0m"
	}
	return s
}

func cmdFind(g *globals, args []string) error {
	fs := newFlagSet(g, "find")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	limit := fs.Int("limit", 0, "maximum number of results")
	all := fs.Bool("all", false, "include disabled packs and skills")
	var packs multiFlag
	fs.Var(&packs, "pack", "restrict to a pack (repeatable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return usageErr("find: a query is required")
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	n := lib.Manifest.SearchLimit()
	if flagSet(fs, "limit") {
		if *limit < 1 {
			return usageErr("find: --limit must be at least 1")
		}
		n = *limit
	}
	recs, warns, err := lib.Scan(g.scope(packs, *all))
	if err != nil {
		return usageErr("%v", err)
	}
	g.warn(warns)
	hits := search.Find(recs, query, n)
	if *asJSON {
		out := make([]recordJSON, len(hits))
		for i, h := range hits {
			out[i] = toJSON(h.Record, h.Score)
		}
		return writeJSONLines(g.env.Stdout, out)
	}
	if len(hits) == 0 {
		fmt.Fprintln(g.env.Stdout, "no matches")
		return nil
	}
	for _, h := range hits {
		g.printHit(h.Record, h.Score)
	}
	return nil
}

func (g *globals) printHit(r *library.Record, score int) {
	w := g.env.Stdout
	head := fmt.Sprintf("%s  [%s]", g.bold(r.ID), r.Trust)
	if score > 0 {
		head = fmt.Sprintf("%3d  %s", score, head)
	}
	fmt.Fprintln(w, head)
	if r.Description != "" {
		fmt.Fprintf(w, "     %s\n", escape(r.Description))
	}
	fmt.Fprintf(w, "     %s\n", g.dim(r.Path))
	for _, wn := range r.Warnings {
		fmt.Fprintf(w, "     warning: %s\n", wn)
	}
}

// flagSet reports whether the flag was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func cmdList(g *globals, args []string) error {
	fs := newFlagSet(g, "list")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	all := fs.Bool("all", false, "include disabled packs and skills")
	trust := fs.String("trust", "", "only skills of this declared trust")
	var packs multiFlag
	fs.Var(&packs, "pack", "restrict to a pack (repeatable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("list: unexpected argument %q", fs.Arg(0))
	}
	switch manifest.Trust(*trust) {
	case "", manifest.Trusted, manifest.Pinned, manifest.Untrusted:
	default:
		if *trust == "modified" {
			return usageErr("list: modified is computed by `skillmeister doctor`, not stored")
		}
		return usageErr("list: unknown trust %q (want trusted, pinned, or untrusted)", *trust)
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	recs, warns, err := lib.Scan(g.scope(packs, *all))
	if err != nil {
		return usageErr("%v", err)
	}
	g.warn(warns)
	if *trust != "" {
		kept := recs[:0]
		for _, r := range recs {
			if string(r.Trust) == *trust {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if (a.Pack == manifest.OwnPack) != (b.Pack == manifest.OwnPack) {
			return a.Pack == manifest.OwnPack
		}
		if a.Pack != b.Pack {
			return a.Pack < b.Pack
		}
		return a.Name < b.Name
	})
	if *asJSON {
		out := make([]recordJSON, len(recs))
		for i, r := range recs {
			out[i] = toJSON(r, 0)
		}
		return writeJSONLines(g.env.Stdout, out)
	}
	for _, r := range recs {
		g.printHit(r, 0)
	}
	return nil
}

func cmdPath(g *globals, args []string) error {
	fs := newFlagSet(g, "path")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("path: exactly one skill ID is required")
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	rec, also, err := g.resolve(lib, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(also) > 0 {
		return usageErr("path: %q is ambiguous; use one of: %s", fs.Arg(0), idList(append([]*library.Record{rec}, also...)))
	}
	fmt.Fprintln(g.env.Stdout, rec.Path)
	return nil
}

func idList(recs []*library.Record) string {
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	return strings.Join(ids, ", ")
}

// resolve scans the enabled skills in scope and resolves one reference.
func (g *globals) resolve(lib *library.Library, ref string) (*library.Record, []*library.Record, error) {
	recs, warns, err := lib.Scan(g.scope(nil, false))
	if err != nil {
		return nil, nil, usageErr("%v", err)
	}
	g.warn(warns)
	rec, also, err := library.Resolve(recs, ref)
	if err != nil {
		return nil, nil, usageErr("%v", err)
	}
	return rec, also, nil
}

func cmdShow(g *globals, args []string) error {
	fs := newFlagSet(g, "show")
	summary := fs.Bool("summary", false, "metadata and file inventory, no body")
	raw := fs.Bool("raw", false, "body only, unescaped")
	meta := fs.Bool("meta", false, "frontmatter only")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErr("show: exactly one skill ID or name is required")
	}
	n := 0
	for _, b := range []bool{*summary, *raw, *meta} {
		if b {
			n++
		}
	}
	if n > 1 {
		return usageErr("show: use at most one of --summary, --raw, --meta")
	}
	lib, err := g.openLibrary()
	if err != nil {
		return err
	}
	defer lib.Close()
	rec, also, err := g.resolve(lib, fs.Arg(0))
	if err != nil {
		return err
	}
	data, err := lib.ReadSkill(rec)
	if err != nil {
		return ioErr(fmt.Errorf("read %s: %w", rec.ID, err))
	}
	fm, _, body, err := library.ParseSkill(data)
	if err != nil {
		return ioErr(fmt.Errorf("%s: %w", rec.ID, err))
	}
	w := g.env.Stdout
	if *raw {
		_, err := w.Write(body)
		if err != nil {
			return ioErr(err)
		}
		return nil
	}
	if *meta {
		fmt.Fprint(w, escape(fm.Raw))
		if !strings.HasSuffix(fm.Raw, "\n") {
			fmt.Fprintln(w)
		}
		return nil
	}
	g.printMeta(rec, also)
	if *summary {
		files, err := lib.Inventory(rec)
		if err != nil {
			return ioErr(err)
		}
		fmt.Fprintf(w, "files: %d\n", len(files))
		for _, f := range files {
			tag := ""
			if f.Script {
				tag = "  [script]"
			}
			if f.Symlink {
				tag += "  [symlink]"
			}
			fmt.Fprintf(w, "  %s  %s%s\n", escape(f.Path), humanSize(f.Size), tag)
		}
		return nil
	}
	fmt.Fprintln(w, "---")
	fmt.Fprint(w, escape(string(body)))
	return nil
}

func (g *globals) printMeta(r *library.Record, also []*library.Record) {
	w := g.env.Stdout
	fmt.Fprintf(w, "id: %s\n", r.ID)
	fmt.Fprintf(w, "pack: %s\n", r.Pack)
	fmt.Fprintf(w, "trust: %s\n", r.Trust)
	fmt.Fprintf(w, "description: %s\n", escape(r.Description))
	fmt.Fprintf(w, "path: %s\n", r.Path)
	if r.FrontmatterName != "" && r.FrontmatterName != r.Name {
		fmt.Fprintf(w, "frontmatter-name: %s\n", escape(r.FrontmatterName))
	}
	if len(also) > 0 {
		fmt.Fprintf(w, "also: %s\n", idList(also))
	}
	for _, wn := range r.Warnings {
		fmt.Fprintf(w, "warning: %s\n", wn)
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// escape makes text safe to print to a terminal. It keeps newline and tab and
// replaces other control and format characters, and invalid bytes, with
// visible escapes.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			b.WriteString(`\r`)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			fmt.Fprintf(&b, `\u{%x}`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
