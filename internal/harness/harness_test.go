package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/doctor"
	"github.com/shantanugoel/skillmeister/internal/library"
)

var tmpl = string(Template)

func TestRender(t *testing.T) {
	out, err := Render([]byte(tmpl), "/bin/sm", "/lib")
	if err != nil || strings.Contains(string(out), "{{") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{`"/bin/sm" --library "/lib" find`, `"/bin/sm" --library "/lib" path <pack>/<name>`, "${CLAUDE_SKILL_DIR}", "<SKILLS>"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the shim lacks %s", want)
		}
	}
	for _, bad := range [][2]string{
		{"rel/sm", "/lib"},
		{"/bin/sm", "lib"},
		{`/bin/"x`, "/lib"},
		{"/bin/sm", "/li\nb"},
		{"/bin/sm", "/li\rb"},
		{"/bin/sm$(id)", "/lib"},
		{"/bin/sm", "/lib/$HOME"},
		{"/bin/`id`", "/lib"},
		{"/bin/sm", `/lib/trail\`},
	} {
		if _, err := Render([]byte(tmpl), bad[0], bad[1]); err == nil {
			t.Errorf("Render(%q, %q) must fail", bad[0], bad[1])
		}
	}
}

func setup(t *testing.T) (home string, lib *library.Library, bin string) {
	t.Helper()
	dir := t.TempDir()
	// The temp dir may sit behind a symlink; use the physical path as install does.
	dir, _ = filepath.EvalSymlinks(dir)
	home = filepath.Join(dir, "home")
	libDir := filepath.Join(dir, "lib")
	for p, c := range map[string]string{
		filepath.Join(libDir, "skillmeister.yaml"):  "version: 1\npacks:\n  own:\n    fetch: local\n    trust: trusted\n",
		filepath.Join(libDir, "skills", ".gitkeep"): "",
	} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	bin = filepath.Join(dir, "bin", "sm")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	var err error
	if lib, err = library.Open(libDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return home, lib, bin
}

func install(t *testing.T, home string, lib *library.Library, bin string) {
	t.Helper()
	out, _ := Render([]byte(tmpl), bin, lib.Abs)
	for _, p := range ShimPaths(home) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, out, 0o644)
	}
}

func codes(fs []doctor.Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.Code)
	}
	return strings.Join(s, ",")
}

func TestCheck(t *testing.T) {
	home, lib, bin := setup(t)
	if got := Check("", lib); got != nil {
		t.Errorf("empty home must skip: %v", got)
	}
	if got := codes(Check(home, lib)); got != "shim-missing,shim-missing" {
		t.Errorf("nothing installed: %s", got)
	}
	install(t, home, lib, bin)
	if got := Check(home, lib); len(got) != 0 {
		t.Fatalf("clean install has findings: %v", got)
	}

	shim := ShimPaths(home)[1]
	orig, _ := os.ReadFile(shim)
	for name, tc := range map[string]struct {
		mutate func()
		want   string
	}{
		"edited":      {func() { os.WriteFile(shim, append(orig, []byte("extra\n")...), 0o644) }, "shim-stale"},
		"no command":  {func() { os.WriteFile(shim, []byte("hello\n"), 0o644) }, "shim-stale"},
		"binary gone": {func() { os.Remove(bin) }, "shim-binary"},
		"not exec":    {func() { os.Chmod(bin, 0o644) }, "shim-binary"},
		"other root": {func() {
			out, _ := Render([]byte(tmpl), bin, filepath.Join(filepath.Dir(lib.Abs), "elsewhere"))
			os.WriteFile(shim, out, 0o644)
		}, "shim-root"},
	} {
		t.Run(name, func(t *testing.T) {
			install(t, home, lib, bin)
			os.Chmod(bin, 0o755)
			os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
			tc.mutate()
			if got := codes(Check(home, lib)); !strings.Contains(got, tc.want) {
				t.Errorf("findings %q lack %s", got, tc.want)
			}
		})
	}
}

func TestStableBin(t *testing.T) {
	prefix, _ := filepath.EvalSymlinks(t.TempDir())
	cellar := filepath.Join(prefix, "Cellar", "skillmeister", "0.2.0", "bin", "skillmeister")
	os.MkdirAll(filepath.Dir(cellar), 0o755)
	os.WriteFile(cellar, []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(prefix, "bin"), 0o755)
	link := filepath.Join(prefix, "bin", "skillmeister")
	if err := os.Symlink(cellar, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if got := StableBin(cellar); got != link {
		t.Errorf("StableBin(cellar) = %s, want %s", got, link)
	}
	// A link to something else is not used.
	os.Remove(link)
	other := filepath.Join(prefix, "other")
	os.WriteFile(other, []byte("y"), 0o755)
	os.Symlink(other, link)
	if got := StableBin(cellar); got != cellar {
		t.Errorf("a link to another file was used: %s", got)
	}
	if got := StableBin("/usr/local/bin/skillmeister"); got != "/usr/local/bin/skillmeister" {
		t.Errorf("plain path changed: %s", got)
	}
}
