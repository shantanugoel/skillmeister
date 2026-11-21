package search

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

func rec(pack, name, desc, body string, trust manifest.Trust) *library.Record {
	return &library.Record{ID: pack + "/" + name, Pack: pack, Name: name, Description: desc, Body: body, Trust: trust}
}

func ids(h []Hit) string {
	var s []string
	for _, x := range h {
		s = append(s, fmt.Sprintf("%s=%d", x.Record.ID, x.Score))
	}
	return strings.Join(s, " ")
}

func TestTiers(t *testing.T) {
	recs := []*library.Record{
		rec("own", "pdf-tools", "Work with files.", "", manifest.Trusted),
		rec("own", "report-pdf", "Make reports.", "", manifest.Trusted),
		rec("own", "docs", "Convert a PDF file.", "", manifest.Trusted),
		rec("own", "misc", "Other things.", "Handles pdf input in the body.", manifest.Trusted),
		rec("own", "none", "Nothing here.", "No match.", manifest.Trusted),
	}
	got := ids(Find(recs, "pdf", 0))
	want := "own/pdf-tools=80 own/report-pdf=60 own/docs=40 own/misc=20"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestExactBeforeTokenize(t *testing.T) {
	recs := []*library.Record{
		rec("own", "pdf-tools", "d", "", manifest.Trusted),
		rec("vendor", "pdf-tools", "d", "", manifest.Pinned),
	}
	if got := ids(Find(recs, "PDF-Tools", 0)); got != "own/pdf-tools=100 vendor/pdf-tools=100" {
		t.Fatalf("exact name: %s", got)
	}
	if got := ids(Find(recs, "vendor/pdf-tools", 0)); got != "vendor/pdf-tools=100" {
		t.Fatalf("exact id: %s", got)
	}
	// The same words with other separators are a prefix match, not exact.
	if got := ids(Find(recs, "pdf tools", 0)); got != "own/pdf-tools=80 vendor/pdf-tools=80" {
		t.Fatalf("tokenized: %s", got)
	}
}

func TestEveryTokenMustMatchWinningField(t *testing.T) {
	recs := []*library.Record{
		rec("own", "commit-writer", "Write messages for git.", "", manifest.Trusted),
	}
	for q, want := range map[string]string{
		"commit":          "own/commit-writer=80",
		"writer commit":   "own/commit-writer=60", // all tokens in name, not a prefix
		"commit git":      "",                     // git is only in the description
		"write messages":  "own/commit-writer=40",
		"commit zzz":      "",
		"messages for":    "own/commit-writer=40",
		"commit-writer x": "",
	} {
		if got := ids(Find(recs, q, 0)); got != want {
			t.Errorf("query %q: got %q, want %q", q, got, want)
		}
	}
}

func TestPrefixNeedsLeadingTokens(t *testing.T) {
	recs := []*library.Record{rec("own", "a-b-c", "d", "", manifest.Trusted)}
	for q, want := range map[string]string{"a b": "own/a-b-c=80", "b c": "own/a-b-c=60", "a b c": "own/a-b-c=80", "a b c d": ""} {
		if got := ids(Find(recs, q, 0)); got != want {
			t.Errorf("query %q: got %q, want %q", q, got, want)
		}
	}
}

func TestWordPrefixes(t *testing.T) {
	recs := []*library.Record{
		rec("own", "hyperframes", "Make motion graphics.", "", manifest.Trusted),
		rec("own", "hyperframe", "The exact name.", "", manifest.Trusted),
		rec("own", "notes", "Covers hyperframes workflows.", "", manifest.Trusted),
		rec("own", "misc", "Other things.", "A hyperframes example.", manifest.Trusted),
	}
	// The singular query finds the plural skill, but the exact name still wins.
	for _, q := range []string{"hyperframe", "hyperframes"} {
		got := ids(Find(recs, q, 0))
		want := "own/hyperframe=100 own/hyperframes=80 own/notes=40 own/misc=20"
		if q == "hyperframes" {
			want = "own/hyperframes=100 own/hyperframe=80 own/notes=40 own/misc=20"
		}
		if got != want {
			t.Errorf("query %q: got  %s\nwant %s", q, got, want)
		}
	}
}

func TestWordPrefixesNeedEveryToken(t *testing.T) {
	recs := []*library.Record{rec("own", "slides-deck", "d", "", manifest.Trusted)}
	for _, tc := range []struct{ query, want string }{
		{"slide deck", "own/slides-deck=80"},
		{"slides decks", "own/slides-deck=80"},
		{"slide deck zzz", ""},
	} {
		if got := ids(Find(recs, tc.query, 0)); got != tc.want {
			t.Errorf("query %q: got %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestWordPrefixBoundaries(t *testing.T) {
	one := func(name string) []*library.Record {
		return []*library.Record{rec("own", name, "d", "", manifest.Trusted)}
	}
	for _, tc := range []struct{ label, name, query, want string }{
		{"four-character query", "testing-tools", "test", "own/testing-tools=80"},
		{"forward long extension", "accessibility-audit", "access", "own/accessibility-audit=80"},
		{"reverse one-char suffix", "hyperframe", "hyperframes", "own/hyperframe=80"},
		{"reverse two-char suffix", "watch", "watches", "own/watch=80"},
		{"reverse three-char suffix", "watch", "watching", ""},
		{"non-leading name token", "git-worktrees", "worktree", "own/git-worktrees=60"},
		{"short field token", "cat", "catalog", ""},
		{"long reverse prefix", "data-tools", "database", ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if got := ids(Find(one(tc.name), tc.query, 0)); got != tc.want {
				t.Errorf("query %q against %q: got %q, want %q", tc.query, tc.name, got, tc.want)
			}
		})
	}
}

func TestLongQueryDoesNotOutrankExactToken(t *testing.T) {
	recs := []*library.Record{
		rec("own", "data-tools", "d", "", manifest.Trusted),
		rec("own", "sql-database", "d", "", manifest.Trusted),
	}
	if got := ids(Find(recs, "database", 0)); got != "own/sql-database=60" {
		t.Errorf("got %q, want own/sql-database=60", got)
	}
}

func TestShortTokensMatchExactly(t *testing.T) {
	recs := []*library.Record{rec("own", "catalog", "A pdf index.", "", manifest.Trusted)}
	if got := ids(Find(recs, "cat", 0)); got != "" {
		t.Errorf("query %q: got %q, want no match", "cat", got)
	}
	if got := ids(Find(recs, "pdf", 0)); got != "own/catalog=40" {
		t.Errorf("query %q: got %q, want own/catalog=40", "pdf", got)
	}
}

func TestTieBreaks(t *testing.T) {
	recs := []*library.Record{
		rec("zeta", "tool-x", "d", "", manifest.Untrusted),
		rec("alpha", "tool-b", "d", "", manifest.Pinned),
		rec("alpha", "tool-a", "d", "", manifest.Pinned),
		rec("beta", "tool-t", "d", "", manifest.Trusted),
		rec("own", "tool-z", "d", "", manifest.Untrusted),
	}
	got := ids(Find(recs, "tool", 0))
	want := "own/tool-z=80 beta/tool-t=80 alpha/tool-a=80 alpha/tool-b=80 zeta/tool-x=80"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestLimit(t *testing.T) {
	recs := []*library.Record{
		rec("own", "x-one", "d", "", manifest.Trusted),
		rec("own", "x-two", "d", "", manifest.Trusted),
		rec("own", "other", "d", "x appears here", manifest.Trusted),
	}
	if got := ids(Find(recs, "x", 2)); got != "own/x-one=80 own/x-two=80" {
		t.Fatalf("limit 2: %s", got)
	}
	if got := ids(Find(recs, "x", 5)); got != "own/x-one=80 own/x-two=80 own/other=20" {
		t.Fatalf("limit 5: %s", got)
	}
}

func TestBodyTierIsSkippedWhenLimitIsFull(t *testing.T) {
	recs := []*library.Record{
		rec("own", "x-one", "d", "", manifest.Trusted),
		rec("own", "x-two", "d", "", manifest.Trusted),
		rec("own", "other", "d", "x appears here", manifest.Trusted),
	}
	var st stats
	find(recs, "x", 2, &st)
	if st.bodyScans != 0 {
		t.Errorf("body tier ran %d times with a full limit", st.bodyScans)
	}
	st = stats{}
	find(recs, "x", 5, &st)
	if st.bodyScans != 1 {
		t.Errorf("body tier ran %d times, want 1", st.bodyScans)
	}
	st = stats{}
	find(recs, "x", 0, &st)
	if st.bodyScans != 1 {
		t.Errorf("no limit: body tier ran %d times, want 1", st.bodyScans)
	}
}

func TestEmptyAndSymbolQueries(t *testing.T) {
	recs := []*library.Record{rec("own", "a", "d", "b", manifest.Trusted)}
	for _, q := range []string{"", "   ", "---", "!!"} {
		if h := Find(recs, q, 0); len(h) != 0 {
			t.Errorf("query %q matched %v", q, ids(h))
		}
	}
}

func TestTokenize(t *testing.T) {
	got := strings.Join(Tokenize("Hello, WORLD-42_x é!"), "|")
	if got != "hello|world|42|x" {
		t.Fatalf("got %q", got)
	}
}

func TestFindPartial(t *testing.T) {
	recs := []*library.Record{
		rec("own", "video-cut", "Trim clips.", "", manifest.Trusted),
		rec("own", "slides", "Make a video deck.", "", manifest.Trusted),
		rec("own", "notes", "Take notes.", "Talks about slides and video.", manifest.Trusted),
		rec("own", "none", "Nothing.", "No match.", manifest.Trusted),
		rec("own", "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg", "d", "", manifest.Trusted),
	}
	for q, want := range map[string]string{
		// video-cut: name 3. slides: description 2. notes: body 1.
		"video zzzz": "own/video-cut=3 own/slides=2 own/notes=1",
		// Repeated and short tokens add nothing.
		"video video to a zzzz": "own/video-cut=3 own/slides=2 own/notes=1",
		// Every token matches, but not in one field: the top partial score.
		"slides video": "own/notes=19 own/slides=19 own/video-cut=3",
		// A record that misses a token is capped at 18.
		"aaaa bbbb cccc dddd eeee ffff gggg zzzz": "own/aaaa-bbbb-cccc-dddd-eeee-ffff-gggg=18",
		"to a": "",
		"  ":   "",
	} {
		if got := ids(FindPartial(recs, q, 0)); got != want {
			t.Errorf("query %q:\n got  %s\n want %s", q, got, want)
		}
	}
	if got, want := ids(FindPartial(recs, "slides video", 1)), "own/notes=19"; got != want {
		t.Errorf("limit: got %s, want %s", got, want)
	}
	if got := ids(Find(recs, "video zzzz", 0)); got != "" {
		t.Fatalf("strict find must miss: %s", got)
	}
}
