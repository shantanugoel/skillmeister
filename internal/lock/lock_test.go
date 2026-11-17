package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkTree(t *testing.T, files map[string]string) *os.Root {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, "pack", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func hash(t *testing.T, root *os.Root) string {
	t.Helper()
	h, err := HashTree(root, "pack")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashRecipeVector(t *testing.T) {
	// This vector pins recipe v1. A change here is a lockfile break.
	root := mkTree(t, map[string]string{"a/SKILL.md": "hello", "a/x.txt": "x"})
	got := hash(t, root)
	// Computed by an independent Python implementation of the recipe.
	const want = "sha256:f12002f24e1c0c68737333ecc2c747dce5c7b8f93899bc6f9025754cc32305ef"
	if got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

func TestHashProperties(t *testing.T) {
	base := map[string]string{"a/SKILL.md": "hello", "b/SKILL.md": "world"}
	h0 := hash(t, mkTree(t, base))

	if h := hash(t, mkTree(t, base)); h != h0 {
		t.Error("same tree, different hash")
	}
	// Content change.
	if h := hash(t, mkTree(t, map[string]string{"a/SKILL.md": "hellO", "b/SKILL.md": "world"})); h == h0 {
		t.Error("content change not detected")
	}
	// Rename.
	if h := hash(t, mkTree(t, map[string]string{"a/SKILL.md": "hello", "c/SKILL.md": "world"})); h == h0 {
		t.Error("rename not detected")
	}
	// Framing: moving bytes between path and content must change the hash.
	h1 := hash(t, mkTree(t, map[string]string{"ab": "c"}))
	h2 := hash(t, mkTree(t, map[string]string{"a": "bc"}))
	if h1 == h2 {
		t.Error("framing is ambiguous")
	}
	// Excluded names do not count, at any depth.
	withJunk := map[string]string{"a/SKILL.md": "hello", "b/SKILL.md": "world", ".DS_Store": "x",
		"a/.DS_Store": "y", ".git/HEAD": "ref", "a/.import/z": "z", ".removed/q": "q"}
	if h := hash(t, mkTree(t, withJunk)); h != h0 {
		t.Error("excluded names changed the hash")
	}
	// Empty directories do not count.
	root := mkTree(t, base)
	if err := root.MkdirAll("pack/empty/deeper", 0o755); err != nil {
		t.Fatal(err)
	}
	if hash(t, root) != h0 {
		t.Error("an empty directory changed the hash")
	}
}

func TestHashExecAndSymlink(t *testing.T) {
	root := mkTree(t, map[string]string{"run.sh": "#!/bin/sh\n"})
	h0 := hash(t, root)
	full := filepath.Join(root.Name(), "pack", "run.sh")
	if err := os.Chmod(full, 0o755); err != nil {
		t.Fatal(err)
	}
	h1 := hash(t, root)
	if h1 == h0 {
		t.Error("the executable bit is not hashed")
	}
	if err := os.Symlink("run.sh", filepath.Join(root.Name(), "pack", "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	h2 := hash(t, root)
	if h2 == h1 {
		t.Error("a symlink is not hashed")
	}
	// A different target must change the hash; a file with the same bytes as
	// the target must hash differently from a symlink.
	os.Remove(filepath.Join(root.Name(), "pack", "link"))
	os.Symlink("other", filepath.Join(root.Name(), "pack", "link"))
	if hash(t, root) == h2 {
		t.Error("the symlink target is not hashed")
	}
}

func TestLockfileRoundTrip(t *testing.T) {
	root := mkTree(t, nil)
	f, err := Read(root)
	if err != nil || len(f.Packs) != 0 {
		t.Fatalf("missing lockfile: %v %v", f, err)
	}
	f.Packs["b"] = Entry{Fetch: "git", Resolved: strings.Repeat("a", 40), Tree: HashPrefix + strings.Repeat("0", 64)}
	f.Packs["a"] = Entry{Fetch: "npx", Resolved: strings.Repeat("b", 40), Tree: HashPrefix + strings.Repeat("1", 64)}
	if err := f.Write(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root.Name(), FileName))
	if strings.Index(string(data), `"a"`) > strings.Index(string(data), `"b"`) || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("output is not canonical:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(root.Name(), FileName+".tmp")); err == nil {
		t.Error("temp file left behind")
	}
	g, err := Read(root)
	if err != nil || g.Packs["a"] != f.Packs["a"] || g.Packs["b"] != f.Packs["b"] {
		t.Fatalf("round trip: %v %v", g, err)
	}
}

func TestLockfileRejectsBadInput(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field": `{"version":1,"packs":{},"extra":1}`,
		"bad version":   `{"version":2,"packs":{}}`,
		"local entry":   `{"version":1,"packs":{"x":{"fetch":"local","resolved":"abcdef1","tree":"sha256:` + strings.Repeat("0", 64) + `"}}}`,
		"bad commit":    `{"version":1,"packs":{"x":{"fetch":"git","resolved":"zzz","tree":"sha256:` + strings.Repeat("0", 64) + `"}}}`,
		"bad tree":      `{"version":1,"packs":{"x":{"fetch":"git","resolved":"abcdef1","tree":"nope"}}}`,
		"not json":      `version: 1`,
	} {
		t.Run(name, func(t *testing.T) {
			root := mkTree(t, nil)
			os.WriteFile(filepath.Join(root.Name(), FileName), []byte(content), 0o644)
			if _, err := Read(root); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestWriterLock(t *testing.T) {
	root := mkTree(t, nil)
	w, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); err != ErrBusy {
		t.Fatalf("second Acquire = %v, want ErrBusy", err)
	}
	w.Release()
	w2, err := Acquire(root)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	w2.Release()
}
