package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testManifest = `version: 1
packs:
  own:
    fetch: local
    path: skills
    trust: trusted
  vendor:
    fetch: git
    url: https://example.com/v.git
    trust: pinned
    disable: [off-skill]
  cold:
    fetch: local
    trust: untrusted
    enabled: false
  gone:
    fetch: local
profiles:
  work: [own, vendor]
`

func skill(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody of " + name + "\n"
}

// mkLib writes files (slash paths) under a temp dir and opens it as a library.
func mkLib(t *testing.T, files map[string]string) *Library {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["skillmeister.yaml"]; !ok {
		files["skillmeister.yaml"] = testManifest
	}
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

func ids(recs []*Record) string {
	var s []string
	for _, r := range recs {
		s = append(s, r.ID)
	}
	return strings.Join(s, ",")
}

func TestScanBasics(t *testing.T) {
	lib := mkLib(t, map[string]string{
		"skills/alpha/SKILL.md":           skill("alpha", "First."),
		"skills/Bad_Name/SKILL.md":        skill("x", "Not normalized."),
		"skills/.hidden/SKILL.md":         skill("h", "Hidden."),
		"skills/no-file/README.md":        "no skill file here",
		"skills/broken/SKILL.md":          "---\nname: [oops\n---\n",
		"skills/alpha/nested/SKILL.md":    skill("nested", "Nested is not a skill."),
		"skills/loose.md":                 "a file, not a directory",
		"packs/vendor/beta/SKILL.md":      skill("beta", "Second."),
		"packs/vendor/off-skill/SKILL.md": skill("off", "Disabled by name."),
		"packs/cold/gamma/SKILL.md":       skill("gamma", "In a disabled pack."),
		"packs/vendor/plain/SKILL.md":     "# No frontmatter\n\nJust text.\n",
	})
	recs, warns, err := lib.Scan(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(recs), "own/alpha,vendor/beta,vendor/plain"; got != want {
		t.Fatalf("ids = %s, want %s", got, want)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{"Bad_Name: directory name is not normalized", "own/broken", "pack gone: directory packs/gone is missing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
	for _, r := range recs {
		if !filepath.IsAbs(r.Path) || !strings.HasSuffix(r.Path, "SKILL.md") {
			t.Errorf("bad path %q", r.Path)
		}
		if r.Warnings == nil {
			t.Errorf("%s: warnings must not be nil", r.ID)
		}
	}
	if r := byID(recs, "vendor/plain"); r.HasFrontmatter || r.Description != "" {
		t.Errorf("no-frontmatter skill: %+v", r)
	}

	all, _, err := lib.Scan(Scope{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), "cold/gamma,own/alpha,vendor/beta,vendor/off-skill,vendor/plain"; got != want {
		t.Fatalf("--all ids = %s", got)
	}
}

func byID(recs []*Record, id string) *Record {
	for _, r := range recs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func TestScopeFilters(t *testing.T) {
	lib := mkLib(t, map[string]string{
		"skills/a/SKILL.md":       skill("a", "A."),
		"packs/vendor/b/SKILL.md": skill("b", "B."),
		"packs/cold/c/SKILL.md":   skill("c", "C."),
	})
	cases := []struct {
		name  string
		scope Scope
		want  string
		err   string
	}{
		{"profile", Scope{Profile: "work"}, "own/a,vendor/b", ""},
		{"pack", Scope{Packs: []string{"vendor"}}, "vendor/b", ""},
		{"profile and pack intersect", Scope{Profile: "work", Packs: []string{"vendor", "cold"}}, "vendor/b", ""},
		{"disabled pack needs all", Scope{Packs: []string{"cold"}}, "", ""},
		{"disabled pack with all", Scope{Packs: []string{"cold"}, All: true}, "cold/c", ""},
		{"unknown profile", Scope{Profile: "nope"}, "", "unknown profile"},
		{"unknown pack", Scope{Packs: []string{"nope"}}, "", "unknown pack"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recs, _, err := lib.Scan(c.scope)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(recs); got != c.want {
				t.Fatalf("ids = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSymlinkEscape is the one escape test: a symlink that leaves the library
// must fail at runtime, and the scan must not read through it.
func TestSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(skill("stolen", "Outside the library.")), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := mkLib(t, map[string]string{
		"skills/ok/SKILL.md": skill("ok", "Inside."),
	})
	// A skill directory that links outside: not followed, so not a skill.
	if err := os.Symlink(outside, filepath.Join(lib.Abs, "skills", "linked-dir")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// A real directory whose SKILL.md links outside: os.Root must refuse it.
	if err := os.MkdirAll(filepath.Join(lib.Abs, "skills", "linked-file"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "SKILL.md"), filepath.Join(lib.Abs, "skills", "linked-file", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	recs, warns, err := lib.Scan(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(recs); got != "own/ok" {
		t.Fatalf("ids = %q, want only own/ok", got)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "own/linked-file") {
		t.Errorf("escape was not reported: %v", warns)
	}
	if _, err := lib.ReadSkill(&Record{Dir: "skills/linked-file"}); err == nil {
		t.Error("ReadSkill followed an escaping symlink")
	}
}

func TestSizeCap(t *testing.T) {
	lib := mkLib(t, map[string]string{
		"skills/huge/SKILL.md": "---\nname: huge\n---\n" + strings.Repeat("x", MaxSkillFile),
		"skills/fine/SKILL.md": skill("fine", "Fine."),
	})
	recs, warns, _ := lib.Scan(Scope{Packs: []string{"own"}})
	if ids(recs) != "own/fine" {
		t.Fatalf("ids = %s", ids(recs))
	}
	if !strings.Contains(strings.Join(warns, "\n"), "larger than 1 MB") {
		t.Fatalf("warnings = %v", warns)
	}
}

func TestResolve(t *testing.T) {
	lib := mkLib(t, map[string]string{
		"skills/dup/SKILL.md":        skill("dup", "Mine."),
		"packs/vendor/dup/SKILL.md":  skill("dup", "Theirs."),
		"packs/vendor/solo/SKILL.md": skill("solo", "Only."),
	})
	recs, _, _ := lib.Scan(Scope{})
	r, also, err := Resolve(recs, "dup")
	if err != nil || r.ID != "own/dup" || ids(also) != "vendor/dup" {
		t.Fatalf("bare dup: %v %v %v", r, ids(also), err)
	}
	r, also, err = Resolve(recs, "vendor/dup")
	if err != nil || r.ID != "vendor/dup" || len(also) != 0 {
		t.Fatalf("full id: %v %v %v", r, ids(also), err)
	}
	if _, _, err := Resolve(recs, "nope"); err == nil {
		t.Fatal("missing skill must fail")
	}
	if _, _, err := Resolve(recs, "own/solo"); err == nil {
		t.Fatal("wrong pack must fail")
	}
}

func TestInventory(t *testing.T) {
	lib := mkLib(t, map[string]string{
		"skills/s/SKILL.md":        skill("s", "S."),
		"skills/s/scripts/run.sh":  "#!/bin/sh\n",
		"skills/s/refs/notes.md":   "notes",
		"skills/s/refs/deep/a.txt": "a",
	})
	recs, _, _ := lib.Scan(Scope{})
	files, err := lib.Inventory(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		tag := ""
		if f.Script {
			tag = "*"
		}
		got = append(got, f.Path+tag)
	}
	want := "SKILL.md,refs/deep/a.txt,refs/notes.md,scripts/run.sh*"
	if strings.Join(got, ",") != want {
		t.Fatalf("inventory = %v, want %s", got, want)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{"pdf_tools": "pdf-tools", "A.B": "a-b", "ok-name": "ok-name"} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
