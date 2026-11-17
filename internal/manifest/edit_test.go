package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const editSrc = `# Skillmeister manifest
version: 1

packs:
  # my own skills
  own:
    fetch: local
    path: skills
    trust: trusted # mine
    enabled: true

  anthropic:
    fetch: npx
    source: anthropics/skills
    ref: 8f3c2a1
    trust: pinned
    enabled: true
    disable: [pdf-forms] # bare skill names

  superpowers:
    fetch: git
    url: https://github.com/obra/superpowers
    ref: v4.2.0
    trust: pinned
    enabled: false

profiles:
  work: [own, anthropic]

defaults:
  search_limit: 8
`

func doc(t *testing.T, src string) *Doc {
	t.Helper()
	d, err := ParseDoc("skillmeister.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestRoundTripGolden checks that an unedited document comes back byte for byte.
func TestRoundTripGolden(t *testing.T) {
	if got := string(doc(t, editSrc).Bytes()); got != editSrc {
		t.Fatalf("round trip changed the text:\n--- got\n%s\n--- want\n%s", got, editSrc)
	}
}

func TestEdits(t *testing.T) {
	tests := []struct {
		name string
		do   func(d *Doc) error
		// each want substring must be in the output; each lack must not
		want, lack []string
	}{
		{"enable", func(d *Doc) error { return d.SetEnabled("superpowers", true) },
			[]string{"    ref: v4.2.0\n    trust: pinned\n    enabled: true\n\nprofiles:"}, nil},
		{"disable pack", func(d *Doc) error { return d.SetEnabled("own", false) },
			[]string{"trust: trusted # mine\n    enabled: false\n"}, nil},
		{"enabled added when absent", func(d *Doc) error {
			if err := d.SetEnabled("anthropic", false); err != nil {
				return err
			}
			return d.SetTrust("own", Untrusted)
		}, []string{"enabled: false\n    disable: [pdf-forms] # bare skill names", "trust: untrusted # mine"}, nil},
		{"trust keeps comment", func(d *Doc) error { return d.SetTrust("own", Untrusted) },
			[]string{"trust: untrusted # mine"}, []string{"trust: trusted"}},
		{"set ref", func(d *Doc) error { return d.SetField("superpowers", "ref", "v5.0.0") },
			[]string{"ref: v5.0.0"}, []string{"v4.2.0"}},
		{"numeric looking ref is quoted", func(d *Doc) error { return d.SetField("anthropic", "ref", "1234567") },
			[]string{`ref: "1234567"`}, nil},
		{"disable list replace", func(d *Doc) error { return d.SetDisable("anthropic", []string{"pdf-forms", "docx"}) },
			[]string{"disable: [pdf-forms, docx] # bare skill names"}, nil},
		{"disable list add", func(d *Doc) error { return d.SetDisable("own", []string{"x"}) },
			[]string{"    enabled: true\n    disable: [x]\n"}, nil},
		{"disable list clear", func(d *Doc) error { return d.SetDisable("anthropic", nil) },
			[]string{"enabled: true\n\n  superpowers:"}, []string{"disable", "bare skill names"}},
		{"add pack", func(d *Doc) error {
			return d.AddPack("extra", &Pack{Fetch: FetchGit, URL: "https://example.com/x.git", Ref: "main", Trust: Untrusted})
		}, []string{"  extra:\n    fetch: git\n    url: https://example.com/x.git\n    ref: main\n    trust: untrusted\n    enabled: true\n"}, nil},
		{"remove pack", func(d *Doc) error {
			changed, err := d.RemovePack("anthropic")
			if err == nil && strings.Join(changed, ",") != "work" {
				t.Errorf("changed profiles = %v", changed)
			}
			return err
		}, []string{"work: [own]", "# my own skills", "superpowers:"}, []string{"anthropic:", "8f3c2a1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := doc(t, editSrc)
			if err := tt.do(d); err != nil {
				t.Fatal(err)
			}
			got := string(d.Bytes())
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output lacks %q:\n%s", w, got)
				}
			}
			for _, l := range tt.lack {
				if strings.Contains(got, l) {
					t.Errorf("output has %q:\n%s", l, got)
				}
			}
			// The head comment and key order survive every edit.
			if !strings.HasPrefix(got, "# Skillmeister manifest\nversion: 1\n") {
				t.Errorf("header changed:\n%s", got)
			}
			if _, err := d.Manifest(); err != nil {
				t.Fatalf("edit made an invalid manifest: %v\n%s", err, got)
			}
		})
	}
}

func TestEditErrors(t *testing.T) {
	d := doc(t, editSrc)
	if err := d.SetEnabled("nope", true); err == nil {
		t.Error("unknown pack must fail")
	}
	if err := d.AddPack("own", &Pack{Fetch: FetchLocal}); err == nil {
		t.Error("reserved ID must fail")
	}
	if err := d.AddPack("anthropic", &Pack{Fetch: FetchLocal}); err == nil {
		t.Error("existing pack must fail")
	}
	if err := d.SetField("own", "fetch", "git"); err == nil {
		t.Error("fetch is not editable")
	}
	if _, err := d.RemovePack("nope"); err == nil {
		t.Error("unknown pack must fail")
	}
}

func TestSaveRejectsInvalidEdit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte(editSrc), 0o644)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	d, err := OpenDoc(root)
	if err != nil {
		t.Fatal(err)
	}
	// pinned is not valid for a local pack.
	if err := d.SetTrust("own", Pinned); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(root); err == nil {
		t.Fatal("Save wrote an invalid manifest")
	}
	got, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(got) != editSrc {
		t.Error("a rejected Save changed the file")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".tmp")); err == nil {
		t.Error("temp file left behind")
	}
	d, _ = OpenDoc(root)
	d.SetEnabled("own", false)
	if err := d.Save(root); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(got), "enabled: false") {
		t.Error("Save did not write")
	}
}

func TestEditShapes(t *testing.T) {
	// Empty packs, then add one.
	d := doc(t, "version: 1\npacks: {}\n")
	if err := d.AddPack("a", &Pack{Fetch: FetchLocal, Path: "src"}); err != nil {
		t.Fatal(err)
	}
	m, err := d.Manifest()
	if err != nil || m.Packs["a"] == nil || m.Packs["a"].Path != "src" {
		t.Fatalf("add to empty packs: %v\n%s", err, d.Bytes())
	}
	// Remove the last pack leaves a valid empty mapping.
	if _, err := d.RemovePack("a"); err != nil {
		t.Fatal(err)
	}
	if m, err := d.Manifest(); err != nil || len(m.Packs) != 0 {
		t.Fatalf("remove last pack: %v\n%s", err, d.Bytes())
	}
	// No packs key at all.
	d = doc(t, "version: 1\n")
	if err := d.AddPack("a", &Pack{Fetch: FetchLocal}); err != nil {
		t.Fatal(err)
	}
	if m, err := d.Manifest(); err != nil || m.Packs["a"] == nil {
		t.Fatalf("add without packs key: %v\n%s", err, d.Bytes())
	}
	// Null packs.
	d = doc(t, "version: 1\npacks:\n")
	if err := d.AddPack("a", &Pack{Fetch: FetchLocal}); err != nil {
		t.Fatal(err)
	}
	if m, err := d.Manifest(); err != nil || m.Packs["a"] == nil {
		t.Fatalf("add to null packs: %v\n%s", err, d.Bytes())
	}
}
