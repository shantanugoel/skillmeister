package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shantanugoel/skillmeister/testdata/gen"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	fixOnce sync.Once
	fixDir  string
	fix     *gen.Fixture
	fixErr  error
)

// fixture300 returns the shared 300-skill synthetic library.
func fixture300(t testing.TB) (string, *gen.Fixture) {
	t.Helper()
	fixOnce.Do(func() {
		fixDir, fixErr = os.MkdirTemp("", "skillmeister-fixture-*")
		if fixErr == nil {
			fix, fixErr = gen.Materialize(fixDir, 300)
		}
	})
	if fixErr != nil {
		t.Fatal(fixErr)
	}
	return fixDir, fix
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fixDir != "" {
		os.RemoveAll(fixDir)
	}
	os.Exit(code)
}

type result struct {
	out, err string
	code     int
}

// run runs the CLI in-process against a library.
func run(t testing.TB, lib string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(string) string { return "" },
	}
	full := append([]string{"--library", lib}, args...)
	code := Run(full, env)
	return result{out.String(), errb.String(), code}
}

type rec struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Pack        string   `json:"pack"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Trust       string   `json:"trust"`
	Score       int      `json:"score"`
	Warnings    []string `json:"warnings"`
}

func parseLines(t testing.TB, out string) []rec {
	t.Helper()
	var recs []rec
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var r rec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

func TestFixtureTopThree(t *testing.T) {
	dir, f := fixture300(t)
	if f.Skills != 300 || len(f.Queries) != 20 {
		t.Fatalf("fixture: %d skills, %d queries", f.Skills, len(f.Queries))
	}
	for _, q := range f.Queries {
		r := run(t, dir, "find", "--json", "--limit", "3", "--", q.Query)
		if r.code != 0 {
			t.Fatalf("%q: exit %d: %s", q.Query, r.code, r.err)
		}
		recs := parseLines(t, r.out)
		var got []string
		for _, x := range recs {
			got = append(got, x.ID)
		}
		if !slices.Contains(got, q.Want) {
			t.Errorf("query %q: %s not in top 3 %v", q.Query, q.Want, got)
		}
	}
}

func TestFixtureHasAllCuratedSkills(t *testing.T) {
	dir, _ := fixture300(t)
	r := run(t, dir, "list", "--json")
	var got []string
	for _, x := range parseLines(t, r.out) {
		got = append(got, x.ID)
	}
	if len(got) != 300 {
		t.Fatalf("list returned %d skills, want 300", len(got))
	}
	for _, id := range gen.IDs() {
		if !slices.Contains(got, id) {
			t.Errorf("missing curated skill %s", id)
		}
	}
	if r.err != "" {
		t.Errorf("clean fixture wrote to stderr: %s", r.err)
	}
}

var goldenQueries = []string{"pdf", "review", "semver", "spreadsheets formulas", "zzzzzz"}

func TestGoldenFindJSON(t *testing.T) {
	dir, _ := fixture300(t)
	for _, q := range goldenQueries {
		t.Run(q, func(t *testing.T) {
			r := run(t, dir, "find", "--json", "--limit", "5", "--", q)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.err)
			}
			got := strings.ReplaceAll(r.out, dir, "<ROOT>")
			name := filepath.Join("testdata", "golden", strings.ReplaceAll(q, " ", "_")+".jsonl")
			if *update {
				if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if got != string(want) {
				t.Errorf("golden mismatch for %q\n got: %s\nwant: %s", q, got, want)
			}
		})
	}
}

func TestFindRecordShape(t *testing.T) {
	dir, _ := fixture300(t)
	r := run(t, dir, "find", "--json", "--limit", "1", "--", "pdf-tools")
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.out)), &raw); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "description,id,name,pack,path,score,trust,warnings" {
		t.Fatalf("keys = %s", got)
	}
	if raw["score"].(float64) != 100 || raw["trust"] != "trusted" {
		t.Fatalf("record = %v", raw)
	}
	if _, ok := raw["warnings"].([]any); !ok {
		t.Fatalf("warnings must be an array: %v", raw["warnings"])
	}
	// list omits score.
	r = run(t, dir, "list", "--json", "--pack", "own")
	if strings.Contains(strings.Split(r.out, "\n")[0], `"score"`) {
		t.Fatal("list must omit score")
	}
}

func TestFindPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test")
	}
	dir, _ := fixture300(t)
	const n = 40
	var d []time.Duration
	for i := range n {
		q := []string{"pdf", "semver", "spreadsheets formulas", "zzzzzz"}[i%4] // zzzzzz forces the body tier
		start := time.Now()
		r := run(t, dir, "find", "--json", "--limit", "8", "--", q)
		d = append(d, time.Since(start))
		if r.code != 0 {
			t.Fatalf("exit %d", r.code)
		}
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	p95 := d[n*95/100]
	t.Logf("find p95 over %d runs at 300 skills: %v (max %v)", n, p95, d[n-1])
	if p95 > 100*time.Millisecond {
		t.Errorf("p95 = %v, want under 100ms", p95)
	}
}

func TestUsageErrors(t *testing.T) {
	dir, _ := fixture300(t)
	for _, tt := range []struct {
		name string
		args []string
		msg  string
	}{
		{"no query", []string{"find"}, "query is required"},
		{"bad limit", []string{"find", "--limit", "0", "x"}, "--limit"},
		{"unknown flag", []string{"find", "--nope", "x"}, "invalid flags"},
		{"unknown command", []string{"frobnicate"}, "unknown command"},
		{"unknown profile", []string{"--profile", "nope", "list"}, "unknown profile"},
		{"unknown pack", []string{"list", "--pack", "nope"}, "unknown pack"},
		{"list modified", []string{"list", "--trust", "modified"}, "doctor"},
		{"list bad trust", []string{"list", "--trust", "x"}, "unknown trust"},
		{"show missing", []string{"show", "nope"}, "no skill"},
		{"show two modes", []string{"show", "--raw", "--meta", "own/pdf-tools"}, "at most one"},
		{"path missing", []string{"path"}, "exactly one"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, dir, tt.args...)
			if r.code != ExitUsage || !strings.Contains(r.err, tt.msg) {
				t.Fatalf("code=%d err=%q, want exit 1 with %q", r.code, r.err, tt.msg)
			}
			if r.out != "" {
				t.Errorf("errors must not write stdout: %q", r.out)
			}
		})
	}
	if r := run(t, t.TempDir(), "list"); r.code != ExitUsage || !strings.Contains(r.err, "not a library") {
		t.Fatalf("empty dir: code=%d err=%q", r.code, r.err)
	}
}

func TestFlagsBeforePositionals(t *testing.T) {
	dir, _ := fixture300(t)
	// After the first positional, flags are part of the query.
	r := run(t, dir, "find", "pdf", "--json")
	if r.code != 0 || strings.HasPrefix(strings.TrimSpace(r.out), "{") {
		t.Fatalf("a flag after the query must not switch to JSON: %q", r.out)
	}
	// -- lets the query start with a dash.
	r = run(t, dir, "find", "--json", "--", "-pdf")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
}

func TestShowAndPath(t *testing.T) {
	dir, _ := fixture300(t)
	r := run(t, dir, "show", "pdf-tools")
	if r.code != 0 {
		t.Fatal(r.err)
	}
	for _, want := range []string{"id: own/pdf-tools", "trust: trusted", "path: " + dir, "\n---\n\n# pdf tools"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("show lacks %q:\n%s", want, r.out)
		}
	}
	if r = run(t, dir, "show", "--raw", "own/pdf-tools"); !strings.HasPrefix(r.out, "\n# pdf tools") {
		t.Errorf("raw = %q", r.out[:40])
	}
	if r = run(t, dir, "show", "--meta", "own/pdf-tools"); !strings.HasPrefix(r.out, "name: pdf-tools") || strings.Contains(r.out, "# pdf") {
		t.Errorf("meta = %q", r.out)
	}
	if r = run(t, dir, "show", "--summary", "own/pdf-tools"); !strings.Contains(r.out, "files: 1\n  SKILL.md") || strings.Contains(r.out, "# pdf") {
		t.Errorf("summary = %q", r.out)
	}
	r = run(t, dir, "path", "own/pdf-tools")
	if want := filepath.Join(dir, "skills", "pdf-tools", "SKILL.md") + "\n"; r.out != want {
		t.Errorf("path = %q, want %q", r.out, want)
	}
}

func TestShowEscapesControlCharacters(t *testing.T) {
	dir := t.TempDir()
	write := func(p, c string) {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skillmeister.yaml", "version: 1\npacks:\n  own:\n    fetch: local\n    trust: trusted\n")
	write("skills/evil/SKILL.md", "---\nname: evil\ndescription: Has\\x1b\n---\nline\x1b[31mred\x1b[0m\r\nbidi \u202e done\x00\n")
	r := run(t, dir, "show", "evil")
	if strings.ContainsAny(r.out, "\x1b\x00\r\u202e") {
		t.Fatalf("control characters leaked: %q", r.out)
	}
	if !strings.Contains(r.out, `\u{1b}[31mred`) || !strings.Contains(r.out, `\r`) || !strings.Contains(r.out, `\u{202e}`) {
		t.Errorf("escapes missing: %q", r.out)
	}
	if r = run(t, dir, "show", "--raw", "evil"); !strings.Contains(r.out, "\x1b[31m") {
		t.Errorf("--raw must not escape")
	}
}

func TestAmbiguousBareName(t *testing.T) {
	dir := t.TempDir()
	write := func(p, c string) {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	write("skillmeister.yaml", "version: 1\npacks:\n  own:\n    fetch: local\n    trust: trusted\n  v:\n    fetch: git\n    url: u\n    trust: pinned\n")
	write("skills/dup/SKILL.md", "---\nname: dup\n---\nmine")
	write("packs/v/dup/SKILL.md", "---\nname: dup\n---\ntheirs")
	if r := run(t, dir, "show", "dup"); !strings.Contains(r.out, "id: own/dup") || !strings.Contains(r.out, "also: v/dup") {
		t.Errorf("show dup:\n%s", r.out)
	}
	if r := run(t, dir, "path", "dup"); r.code != ExitUsage || !strings.Contains(r.err, "ambiguous") {
		t.Errorf("path dup: code=%d err=%q", r.code, r.err)
	}
	if r := run(t, dir, "path", "v/dup"); r.code != 0 {
		t.Errorf("path v/dup: %q", r.err)
	}
}

func TestQueryCommandsWriteNothing(t *testing.T) {
	dir, _ := fixture300(t)
	before := snapshot(t, dir)
	for _, args := range [][]string{
		{"find", "pdf"}, {"find", "--json", "--all", "semver"}, {"list"}, {"show", "pdf-tools"},
		{"show", "--summary", "own/pdf-tools"}, {"path", "own/pdf-tools"},
	} {
		run(t, dir, args...)
	}
	if after := snapshot(t, dir); !slices.Equal(before, after) {
		t.Fatal("a query command changed the library")
	}
}

func snapshot(t testing.TB, dir string) []string {
	t.Helper()
	var s []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, _ := d.Info()
		s = append(s, p+"|"+info.ModTime().String()+"|"+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	return s
}

// TestShimCommandLine renders the shim template, extracts its find command
// line exactly as the agent would run it, and runs that line in a shell
// against the real binary.
func TestShimCommandLine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir, _ := fixture300(t)
	bin := filepath.Join(t.TempDir(), "skillmeister")
	build := exec.Command("go", "build", "-o", bin, "github.com/shantanugoel/skillmeister/cmd/skillmeister")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	tmpl, err := os.ReadFile(filepath.Join("..", "..", "shim", "skillmeister", "SKILL.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	shim := strings.NewReplacer("{{BIN}}", bin, "{{ROOT}}", dir).Replace(string(tmpl))
	block := regexp.MustCompile("(?s)```bash\n(.*?)\n```").FindStringSubmatch(shim)
	if block == nil {
		t.Fatal("shim has no bash block")
	}
	line := strings.Replace(block[1], "<keywords>", "pdf forms", 1)
	if !strings.Contains(line, "find --json --limit 8 --") {
		t.Fatalf("unexpected shim line: %s", line)
	}
	cmd := exec.Command("sh", "-c", line)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shim line failed: %v\n%s", err, stderr.String())
	}
	recs := parseLines(t, string(out))
	if len(recs) == 0 || recs[0].ID != "anthropic/pdf-forms" {
		t.Fatalf("shim line returned %v", recs)
	}
	// Every field that the shim reads must be present.
	for _, f := range []string{"id", "name", "description", "path", "trust", "score", "warnings"} {
		if !strings.Contains(string(out), `"`+f+`"`) {
			t.Errorf("record lacks %q", f)
		}
	}
	// A path with spaces must survive the quoting in the shim.
	spaced := filepath.Join(t.TempDir(), "my library")
	if err := os.Symlink(dir, spaced); err != nil {
		t.Skip("symlinks unavailable")
	}
	line2 := strings.NewReplacer(dir, spaced).Replace(line)
	if out, err := exec.Command("sh", "-c", line2).CombinedOutput(); err != nil {
		t.Fatalf("path with spaces: %v\n%s", err, out)
	}
}

func TestVersion(t *testing.T) {
	r := run(t, t.TempDir(), "--version")
	if r.code != 0 || !strings.HasPrefix(r.out, "skillmeister ") {
		t.Fatalf("version: %+v", r)
	}
}
