package manifest

import (
	"strconv"
	"strings"
	"testing"
)

const valid = `version: 1

packs:
  own:
    fetch: local
    path: skills
    trust: trusted
    enabled: true

  anthropic:
    fetch: npx
    source: anthropics/skills
    ref: 8f3c2a1
    trust: pinned
    disable: [pdf-forms]

  superpowers:
    fetch: git
    url: https://github.com/obra/superpowers
    ref: v4.2.0
    path: skills
    trust: pinned
    enabled: false

profiles:
  work: [own, anthropic]

defaults:
  search_limit: 5
`

func TestParseValid(t *testing.T) {
	m, err := Parse("skillmeister.yaml", []byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.PackNames(), ","); got != "anthropic,own,superpowers" {
		t.Fatalf("pack names = %s", got)
	}
	if !m.Packs["own"].IsEnabled() || m.Packs["superpowers"].IsEnabled() {
		t.Fatal("enabled flags wrong")
	}
	if !m.Packs["anthropic"].SkillDisabled("pdf-forms") {
		t.Fatal("disable list lost")
	}
	if m.SearchLimit() != 5 {
		t.Fatalf("limit = %d", m.SearchLimit())
	}
	if got := m.Packs["own"].Dir(); got != "skills" {
		t.Fatalf("own dir = %s", got)
	}
	if got := m.Packs["anthropic"].Dir(); got != "packs/anthropic" {
		t.Fatalf("pack dir = %s", got)
	}
}

func TestDefaults(t *testing.T) {
	m, err := Parse("m.yaml", []byte("version: 1\npacks:\n  x:\n    fetch: local\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := m.Packs["x"]
	if p.Trust != Untrusted || !p.IsEnabled() || m.SearchLimit() != DefaultSearchLimit {
		t.Fatalf("defaults wrong: %+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line int    // expected line, 0 for none
		msg  string // substring
	}{
		{"missing version", "packs:\n  x:\n    fetch: local\n", 0, "version is required"},
		{"bad version", "version: 2\n", 1, "unsupported version"},
		{"unknown top key", "version: 1\nbogus: 1\n", 2, "bogus"},
		{"unknown pack key", "version: 1\npacks:\n  x:\n    fetch: local\n    colour: red\n", 5, "colour"},
		{"duplicate key", "version: 1\npacks:\n  x:\n    fetch: local\n    fetch: git\n", 5, "already defined"},
		{"unknown fetch", "version: 1\npacks:\n  x:\n    fetch: ftp\n", 4, "unknown fetch"},
		{"missing fetch", "version: 1\npacks:\n  x:\n    trust: trusted\n", 3, "fetch is required"},
		{"unknown trust", "version: 1\npacks:\n  x:\n    fetch: local\n    trust: modified\n", 5, "unknown trust"},
		{"pinned local", "version: 1\npacks:\n  x:\n    fetch: local\n    trust: pinned\n", 5, "not valid for a local pack"},
		{"npx needs source", "version: 1\npacks:\n  x:\n    fetch: npx\n", 3, "needs source"},
		{"git needs url", "version: 1\npacks:\n  x:\n    fetch: git\n", 3, "needs url"},
		{"local ref", "version: 1\npacks:\n  x:\n    fetch: local\n    ref: v1\n", 5, "ref is not valid"},
		{"mixed fetch fields", "version: 1\npacks:\n  x:\n    fetch: git\n    url: u\n    source: s\n", 6, "source is only valid"},
		{"bad pack id", "version: 1\npacks:\n  Bad_Name:\n    fetch: local\n", 3, "invalid pack ID"},
		{"own not local", "version: 1\npacks:\n  own:\n    fetch: git\n    url: u\n", 4, "must use fetch local"},
		{"own bad path", "version: 1\npacks:\n  own:\n    fetch: local\n    path: mine\n", 5, "path must be"},
		{"disable pack/skill", "version: 1\npacks:\n  x:\n    fetch: local\n    disable: [x/y]\n", 5, "bare normalized"},
		{"profile unknown pack", "version: 1\npacks:\n  x:\n    fetch: local\nprofiles:\n  p: [y]\n", 6, "unknown pack"},
		{"two documents", "version: 1\n---\nversion: 1\n", 2, "multiple YAML documents"},
		{"empty", "", 0, "empty"},
		{"bad yaml", "version: [1\n", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("skillmeister.yaml", []byte(tt.src))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("error %q lacks %q", err, tt.msg)
			}
			if !strings.HasPrefix(err.Error(), "skillmeister.yaml") {
				t.Errorf("error %q lacks the file name", err)
			}
			if tt.line > 0 {
				want := "[" // go-yaml errors carry [line:col]
				if e, ok := err.(*Error); ok && e.Line > 0 {
					if e.Line != tt.line {
						t.Errorf("line = %d, want %d (%v)", e.Line, tt.line, err)
					}
				} else if !strings.Contains(err.Error(), want+strconv.Itoa(tt.line)+":") {
					t.Errorf("error %q has no line %d", err, tt.line)
				}
			}
		})
	}
}

func TestValidPackID(t *testing.T) {
	for _, bad := range []string{"own", ".import", ".removed", ".", "..", "", "Foo", "a b", "-a", "a/b", "a_b"} {
		if ValidPackID(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, ok := range []string{"a", "anthropic", "pack-2", "9lives"} {
		if err := ValidPackID(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestProfile(t *testing.T) {
	m, _ := Parse("m.yaml", []byte(valid))
	if _, err := m.Profile("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Profile("nope"); err == nil {
		t.Fatal("unknown profile must fail")
	}
}
