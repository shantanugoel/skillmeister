package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/harness"
)

// runHome runs the CLI with HOME set, so init and doctor can see the agent files.
func runHome(t testing.TB, home string, args ...string) result {
	t.Helper()
	var out, errb strings.Builder
	env := Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return ""
		},
	}
	code := Run(args, env)
	return result{out.String(), errb.String(), code}
}

func realPath(t testing.TB, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInitCreatesLibraryAndSetsUpAgents(t *testing.T) {
	home := realPath(t, t.TempDir())
	lib := filepath.Join(home, "lib")
	r := mustOK(t, runHome(t, home, "--library", lib, "init", "--bin", "/opt/sm/skillmeister"))
	for _, want := range []string{"created the library at " + lib, "wrote the shim", "git init"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	for _, f := range []string{"skillmeister.yaml", "skills/.gitkeep", ".gitignore", ".gitattributes"} {
		if !fileExists(filepath.Join(lib, f)) {
			t.Errorf("%s was not created", f)
		}
	}
	want, _ := harness.Render(harness.Template, "/opt/sm/skillmeister", lib)
	for _, p := range harness.ShimPaths(home) {
		if got := readAbs(t, p); got != string(want) {
			t.Errorf("%s is not the rendered shim", p)
		}
	}
	// Agent memory files are not touched.
	if fileExists(filepath.Join(home, ".claude", "CLAUDE.md")) || fileExists(filepath.Join(home, ".config")) {
		t.Error("init wrote agent memory or config files")
	}
	// The new library is valid and doctor passes. The binary path is a fake, so
	// doctor reports it; every other check is clean.
	r = runHome(t, home, "--library", lib, "doctor")
	if !strings.Contains(r.out, "shim-binary") || strings.Contains(r.out, "orphan") {
		t.Errorf("doctor:\n%s", r.out)
	}
	mustOK(t, run(t, lib, "list"))
}

func TestInitIsIdempotentAndKeepsExistingFiles(t *testing.T) {
	home := realPath(t, t.TempDir())
	lib := filepath.Join(home, "lib")
	bin := filepath.Join(home, "bin", "skillmeister")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	mustOK(t, runHome(t, home, "--library", lib, "init", "--bin", bin))
	// The user edits the manifest. A second init changes nothing.
	write(t, lib, "skillmeister.yaml", baseManifest)
	before := tree(t, home)
	r := mustOK(t, runHome(t, home, "--library", lib, "init", "--bin", bin))
	if !strings.Contains(r.out, "already exists") {
		t.Errorf("second init:\n%s", r.out)
	}
	if after := tree(t, home); !equalMaps(before, after) {
		t.Error("a second init changed files")
	}
	mustOK(t, runHome(t, home, "--library", lib, "doctor"))
}

func TestInitRefusals(t *testing.T) {
	t.Run("non-empty directory without a manifest", func(t *testing.T) {
		home := realPath(t, t.TempDir())
		lib := filepath.Join(home, "lib")
		write(t, lib, "notes.txt", "mine")
		r := runHome(t, home, "--library", lib, "init", "--no-shim")
		if r.code != ExitUsage || !strings.Contains(r.err, "not empty") {
			t.Errorf("exit %d: %s", r.code, r.err)
		}
		if fileExists(filepath.Join(lib, "skillmeister.yaml")) {
			t.Error("files were written into a directory that is not empty")
		}
	})
	t.Run("a foreign skill directory blocks everything", func(t *testing.T) {
		home := realPath(t, t.TempDir())
		lib := filepath.Join(home, "lib")
		write(t, filepath.Join(home, ".claude", "skills", "skillmeister"), "SKILL.md", "---\nname: other\n---\nmine\n")
		r := runHome(t, home, "--library", lib, "init", "--bin", "/x/skillmeister")
		if r.code != ExitUsage || !strings.Contains(r.err, "not a skillmeister shim") {
			t.Errorf("exit %d: %s", r.code, r.err)
		}
		if fileExists(lib) || fileExists(filepath.Join(home, ".agents")) {
			t.Error("something was written before the refusal")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		home := realPath(t, t.TempDir())
		os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755)
		os.Symlink(t.TempDir(), filepath.Join(home, ".agents", "skills", "skillmeister"))
		r := runHome(t, home, "--library", filepath.Join(home, "lib"), "init", "--bin", "/x/skillmeister")
		if r.code != ExitUsage || !strings.Contains(r.err, "symlink") {
			t.Errorf("exit %d: %s", r.code, r.err)
		}
	})
	t.Run("no HOME", func(t *testing.T) {
		r := runHome(t, "", "--library", t.TempDir()+"/lib", "init")
		if r.code != ExitUsage || !strings.Contains(r.err, "--no-shim") {
			t.Errorf("exit %d: %s", r.code, r.err)
		}
		mustOK(t, runHome(t, "", "--library", t.TempDir()+"/lib", "init", "--no-shim"))
	})
	t.Run("relative bin", func(t *testing.T) {
		home := realPath(t, t.TempDir())
		lib := filepath.Join(home, "lib")
		for _, bad := range []string{"rel/sm", `/a/"b`} {
			r := runHome(t, home, "--library", lib, "init", "--bin", bad)
			if r.code != ExitUsage {
				t.Errorf("--bin %q: exit %d, want 1", bad, r.code)
			}
			if fileExists(lib) || fileExists(filepath.Join(home, ".claude")) {
				t.Errorf("--bin %q: files were written before the refusal", bad)
			}
		}
	})
}

func TestMissingLibraryHints(t *testing.T) {
	home := realPath(t, t.TempDir())
	// Default location.
	r := runHome(t, home, "find", "pdf")
	if r.code != ExitUsage || !strings.Contains(r.err, "run `skillmeister init` to create a library at ~/.skillmeister") {
		t.Errorf("default: %d %q", r.code, r.err)
	}
	// Explicit location.
	lib := filepath.Join(home, "elsewhere")
	r = runHome(t, home, "--library", lib, "list")
	if !strings.Contains(r.err, "skillmeister --library "+lib+" init") {
		t.Errorf("explicit: %q", r.err)
	}
	// Running with no command shows the usage and the hint.
	r = runHome(t, home)
	if r.code != ExitUsage || !strings.Contains(r.err, "There is no library yet") || !strings.Contains(r.err, "skillmeister init") {
		t.Errorf("no command: %q", r.err)
	}
	// After init, the hint is gone and the default is used.
	mustOK(t, runHome(t, home, "init", "--bin", "/x/skillmeister"))
	if !fileExists(filepath.Join(home, ".skillmeister", "skillmeister.yaml")) {
		t.Fatal("init did not use ~/.skillmeister")
	}
	mustOK(t, runHome(t, home, "list"))
	if r = runHome(t, home); strings.Contains(r.err, "no library yet") {
		t.Errorf("hint after init: %q", r.err)
	}
}

func TestDefaultLibraryFallsBackToTheOldLocation(t *testing.T) {
	home := realPath(t, t.TempDir())
	old := filepath.Join(home, "dev", "skillmeister")
	write(t, old, "skillmeister.yaml", baseManifest)
	write(t, old, "skills/alpha/SKILL.md", skillMD("alpha", "Old place."))
	r := mustOK(t, runHome(t, home, "find", "--json", "alpha"))
	if !strings.Contains(r.out, old) {
		t.Errorf("the old location was not used:\n%s", r.out)
	}
	// Once ~/.skillmeister has a library, it wins.
	write(t, filepath.Join(home, ".skillmeister"), "skillmeister.yaml", baseManifest)
	if r := runHome(t, home, "find", "--json", "alpha"); strings.Contains(r.out, "alpha") {
		t.Errorf("the new default did not win:\n%s", r.out)
	}
	// SKILLMEISTER_ROOT beats both.
	var out, errb strings.Builder
	env := Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Getenv: func(k string) string {
		switch k {
		case "HOME":
			return home
		case "SKILLMEISTER_ROOT":
			return old
		}
		return ""
	}}
	if code := Run([]string{"find", "--json", "alpha"}, env); code != 0 || !strings.Contains(out.String(), "alpha") {
		t.Errorf("SKILLMEISTER_ROOT: %d %s", code, out.String())
	}
}

func TestInitRepairsAnInterruptedShimDirectory(t *testing.T) {
	home := realPath(t, t.TempDir())
	dir := filepath.Join(home, ".claude", "skills", "skillmeister")
	write(t, dir, ".SKILL.md.12345", "half")
	mustOK(t, runHome(t, home, "--library", filepath.Join(home, "lib"), "init", "--bin", "/x/skillmeister"))
	if !fileExists(filepath.Join(dir, "SKILL.md")) {
		t.Error("the shim was not written")
	}
	// A directory with other files is still refused.
	home2 := realPath(t, t.TempDir())
	d2 := filepath.Join(home2, ".claude", "skills", "skillmeister")
	write(t, d2, "notes.txt", "mine")
	if r := runHome(t, home2, "--library", filepath.Join(home2, "lib"), "init", "--bin", "/x/skillmeister"); r.code != ExitUsage {
		t.Errorf("exit %d", r.code)
	}
}

func TestHintOnlyWhereInitCanHelp(t *testing.T) {
	home := realPath(t, t.TempDir())
	busy := filepath.Join(home, "busy dir")
	write(t, busy, "notes.txt", "x")
	r := runHome(t, home, "--library", busy, "list")
	if strings.Contains(r.err, "init") || !strings.Contains(r.err, "has files but no skillmeister.yaml") {
		t.Errorf("busy dir: %q", r.err)
	}
	// A path with a space is quoted in the hint.
	r = runHome(t, home, "--library", filepath.Join(home, "my lib"), "list")
	if !strings.Contains(r.err, "--library '"+filepath.Join(home, "my lib")+"' init") {
		t.Errorf("quoting: %q", r.err)
	}
	// An empty existing directory is fine for init.
	empty := filepath.Join(home, "empty")
	os.MkdirAll(empty, 0o755)
	if r := runHome(t, home, "--library", empty, "list"); !strings.Contains(r.err, "init") {
		t.Errorf("empty dir: %q", r.err)
	}
}
