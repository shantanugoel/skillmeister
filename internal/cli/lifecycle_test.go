package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/lock"
)

const baseManifest = `# my library
version: 1

packs:
  # my own skills
  own:
    fetch: local
    path: skills
    trust: trusted # mine
    enabled: true

profiles:
  all: [own]
`

func write(t testing.TB, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t testing.TB, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func skillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody of " + name + "\n"
}

// newLib creates a small library with two own skills.
func newLib(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "skillmeister.yaml", baseManifest)
	write(t, dir, "skills/alpha/SKILL.md", skillMD("alpha", "Alpha tool for tests."))
	write(t, dir, "skills/beta/SKILL.md", skillMD("beta", "Beta tool for tests."))
	return dir
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitRepo creates a git repo with the files and returns its file:// URL and HEAD.
func gitRepo(t *testing.T, files map[string]string) (url, head string) {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	for p, c := range files {
		write(t, dir, p, c)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return "file://" + dir, git("rev-parse", "HEAD")
}

// tree lists every file under dir with its content, except the named paths.
func tree(t testing.TB, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		for _, s := range skip {
			if rel == s {
				return nil
			}
		}
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustOK(t *testing.T, r result) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.out, r.err)
	}
	return r
}

func ids(t testing.TB, dir string, extra ...string) []string {
	t.Helper()
	r := run(t, dir, append([]string{"list", "--json"}, extra...)...)
	var out []string
	for _, x := range parseLines(t, r.out) {
		out = append(out, x.ID)
	}
	return out
}

func TestEnableDisable(t *testing.T) {
	dir := newLib(t)
	before := tree(t, dir, "skillmeister.yaml", lock.LockName)
	mustOK(t, run(t, dir, "disable", "own/alpha"))
	if got := strings.Join(ids(t, dir), ","); got != "own/beta" {
		t.Fatalf("after disable: %s", got)
	}
	if r := run(t, dir, "find", "--json", "alpha"); strings.TrimSpace(r.out) != "" {
		t.Errorf("find still returns the disabled skill: %s", r.out)
	}
	if r := run(t, dir, "find", "--json", "--all", "alpha"); !strings.Contains(r.out, "own/alpha") {
		t.Errorf("--all must include it: %s", r.out)
	}
	m := read(t, dir, "skillmeister.yaml")
	if !strings.Contains(m, "disable: [alpha]") || !strings.Contains(m, "# my own skills") || !strings.Contains(m, "trust: trusted # mine") {
		t.Errorf("manifest after disable:\n%s", m)
	}
	// Pack-level.
	mustOK(t, run(t, dir, "disable", "own"))
	if got := ids(t, dir); len(got) != 0 {
		t.Fatalf("disabled pack still listed: %v", got)
	}
	mustOK(t, run(t, dir, "enable", "own"))
	mustOK(t, run(t, dir, "enable", "own/alpha"))
	if got := strings.Join(ids(t, dir), ","); got != "own/alpha,own/beta" {
		t.Fatalf("after enable: %s", got)
	}
	if m := read(t, dir, "skillmeister.yaml"); strings.Contains(m, "disable:") {
		t.Errorf("empty disable list left behind:\n%s", m)
	}
	// Idempotent, and nothing but the manifest changed.
	if r := mustOK(t, run(t, dir, "enable", "own")); !strings.Contains(r.out, "already") {
		t.Errorf("not idempotent: %s", r.out)
	}
	if after := tree(t, dir, "skillmeister.yaml", lock.LockName); !equalMaps(before, after) {
		t.Error("enable and disable changed files other than the manifest")
	}
	for _, bad := range [][]string{{"disable", "nope"}, {"disable", "own/nope"}, {"enable"}, {"disable", "own/Bad Name"}} {
		if r := run(t, dir, bad...); r.code != ExitUsage {
			t.Errorf("%v: exit %d, want 1", bad, r.code)
		}
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestAddLocal(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "Foo_Bar/SKILL.md", skillMD("foo", "Foo."))
	write(t, src, "Foo_Bar/scripts/run.sh", "#!/bin/sh\n")
	write(t, src, "plain/SKILL.md", "---\nname: plain\ndescription: Plain.\nallowed-tools: Bash(git:*)\n---\nbody")
	write(t, src, "notaskill/readme.md", "x")
	write(t, src, "README.md", "x")
	r := mustOK(t, run(t, dir, "add", "--as", "mine", src))
	for _, want := range []string{"added pack mine (local, 2 skills, trust untrusted)", "foo-bar", "Foo_Bar -> foo-bar", "needs native semantics: plain", "does not commit"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("summary lacks %q:\n%s", want, r.out)
		}
	}
	if !strings.Contains(read(t, dir, "skillmeister.yaml"), "  mine:\n    fetch: local\n") {
		t.Errorf("manifest:\n%s", read(t, dir, "skillmeister.yaml"))
	}
	if _, err := os.Stat(filepath.Join(dir, "packs", "mine", "foo-bar", "scripts", "run.sh")); err != nil {
		t.Error("files not copied")
	}
	if _, err := os.Stat(filepath.Join(dir, lock.FileName)); err == nil {
		t.Error("a local pack must have no lock entry")
	}
	if got := strings.Join(ids(t, dir, "--pack", "mine"), ","); got != "mine/foo-bar,mine/plain" {
		t.Errorf("ids = %s", got)
	}
	mustOK(t, run(t, dir, "doctor"))
	// Existing pack needs --force.
	if r := run(t, dir, "add", "--as", "mine", src); r.code != ExitUsage || !strings.Contains(r.err, "--force") {
		t.Errorf("duplicate add: %d %s", r.code, r.err)
	}
	write(t, src, "extra/SKILL.md", skillMD("extra", "Extra."))
	mustOK(t, run(t, dir, "add", "--as", "mine", "--force", src))
	if got := strings.Join(ids(t, dir, "--pack", "mine"), ","); got != "mine/extra,mine/foo-bar,mine/plain" {
		t.Errorf("after force: %s", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".removed")); len(entries) != 0 {
		t.Errorf("tombstone left behind: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".import")); len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func TestAddRejectsBadInput(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "a-b/SKILL.md", skillMD("a", "A."))
	write(t, src, "a_b/SKILL.md", skillMD("a", "A."))
	empty := t.TempDir()
	for name, tt := range map[string]struct {
		args []string
		msg  string
	}{
		"collision":      {[]string{"add", "--as", "p", src}, "collide"},
		"no skills":      {[]string{"add", "--as", "p", empty}, "no skills found"},
		"reserved own":   {[]string{"add", "--as", "own", empty}, "reserved"},
		"bad id":         {[]string{"add", "--as", "Bad_ID", empty}, "must match"},
		"local ref":      {[]string{"add", "--as", "p", "--ref", "v1", empty}, "--ref is not valid"},
		"pinned local":   {[]string{"add", "--as", "p", "--trust", "pinned", empty}, "pinned is not valid"},
		"bad fetch":      {[]string{"add", "--fetch", "ftp", empty}, "--fetch"},
		"missing source": {[]string{"add", "--as", "p", "--fetch", "local", "/no/such/dir"}, "no such file"},
		"no arg":         {[]string{"add"}, "exactly one"},
		"dash url":       {[]string{"add", "--as", "p", "--fetch", "git", "--", "-oProxyCommand=x"}, "dash"},
	} {
		t.Run(name, func(t *testing.T) {
			before := tree(t, dir, lock.LockName)
			r := run(t, dir, tt.args...)
			if r.code == 0 || !strings.Contains(r.err, tt.msg) {
				t.Fatalf("exit %d, stderr %q; want failure with %q", r.code, r.err, tt.msg)
			}
			after := tree(t, dir, lock.LockName)
			// An empty .import directory is gitignored and allowed.
			delete(before, ".import/")
			delete(after, ".import/")
			if !equalMaps(before, after) {
				for k := range after {
					if _, ok := before[k]; !ok {
						t.Logf("new: %s", k)
					}
				}
				for k := range before {
					if _, ok := after[k]; !ok {
						t.Logf("gone: %s", k)
					}
				}
				t.Error("a rejected add changed the library")
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, ".import")); len(entries) != 0 {
				t.Errorf("staging left behind: %v", entries)
			}
		})
	}
}

func TestInferFetch(t *testing.T) {
	existing := t.TempDir()
	for src, want := range map[string]string{
		"./x": "local", "../x": "local", "/abs/x": "local", "~/x": "local", existing: "local",
		"git@github.com:o/r.git": "git", "https://x.com/o/r.git": "git", "file:///x/y": "git", "ssh://git@x/y": "git",
		"anthropics/skills": "npx", "https://github.com/o/r": "npx", "notion": "npx",
	} {
		if got := string(inferFetch(src)); got != want {
			t.Errorf("inferFetch(%q) = %s, want %s", src, got, want)
		}
	}
	for src, want := range map[string]string{
		"anthropics/skills": "skills", "https://x.com/o/My_Repo.git": "my-repo", "git@github.com:o/r.git": "r",
		"./some/dir/": "dir", "o/r#v1": "r",
	} {
		if got, err := derivePackID(src); err != nil || got != want {
			t.Errorf("derivePackID(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
}

// gitSkills is the content of a skills repo whose skills sit in skills/.
func gitSkills() map[string]string {
	return map[string]string{
		"README.md":                 "readme",
		"skills/pdf/SKILL.md":       skillMD("pdf", "Work with PDF files."),
		"skills/pdf/scripts/run.py": "print('x')\n",
		"skills/Web_Tool/SKILL.md":  skillMD("web", "Web helper."),
		"skills/notes.md":           "not a skill",
	}
}

func TestGitPackLifecycle(t *testing.T) {
	dir := newLib(t)
	url, head := gitRepo(t, gitSkills())

	r := mustOK(t, run(t, dir, "add", "--as", "vend", url))
	if !strings.Contains(r.out, "added pack vend (git, 2 skills, trust untrusted, resolved "+head[:12]+")") {
		t.Fatalf("summary:\n%s", r.out)
	}
	m := read(t, dir, "skillmeister.yaml")
	if !strings.Contains(m, "url: "+url) || !strings.Contains(m, "path: skills") || !strings.Contains(m, "trust: untrusted") {
		t.Errorf("manifest:\n%s", m)
	}
	var lk lock.File
	if err := json.Unmarshal([]byte(read(t, dir, lock.FileName)), &lk); err != nil {
		t.Fatal(err)
	}
	e := lk.Packs["vend"]
	if e.Fetch != "git" || e.Resolved != head || !strings.HasPrefix(e.Tree, "sha256:") {
		t.Fatalf("lock entry: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs", "vend", "web-tool", "SKILL.md")); err != nil {
		t.Error("normalized skill missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "packs", "vend", ".git")); err == nil {
		t.Error(".git must not be in the pack")
	}
	mustOK(t, run(t, dir, "doctor"))

	// untrusted -> pinned verifies the hash.
	mustOK(t, run(t, dir, "trust", "--level", "pinned", "vend"))
	if !strings.Contains(read(t, dir, "skillmeister.yaml"), "trust: pinned") {
		t.Fatal("trust not written")
	}
	if r := mustOK(t, run(t, dir, "doctor")); !strings.Contains(r.out, "verified") {
		t.Errorf("doctor did not verify:\n%s", r.out)
	}
	mustOK(t, run(t, dir, "sync", "--frozen"))

	// Editing your own trusted skill is not drift.
	write(t, dir, "skills/alpha/SKILL.md", skillMD("alpha", "Edited."))
	mustOK(t, run(t, dir, "doctor"))

	// Hand-editing a pinned pack is drift.
	write(t, dir, "packs/vend/pdf/SKILL.md", skillMD("pdf", "Tampered."))
	r = run(t, dir, "doctor")
	if r.code != ExitDrift || !strings.Contains(r.out, "modified") || !strings.Contains(r.out, "pinned -> MODIFIED") {
		t.Fatalf("doctor on a modified pack: %d\n%s%s", r.code, r.out, r.err)
	}
	// find still reports the declared trust, never modified.
	if out := run(t, dir, "find", "--json", "pdf").out; !strings.Contains(out, `"trust":"pinned"`) {
		t.Errorf("find: %s", out)
	}
	if r := run(t, dir, "sync", "--frozen"); r.code != ExitDrift {
		t.Errorf("frozen exit %d", r.code)
	}
	// sync never overwrites it.
	r = run(t, dir, "sync")
	if r.code != ExitDrift || !strings.Contains(read(t, dir, "packs/vend/pdf/SKILL.md"), "Tampered") {
		t.Errorf("sync overwrote or hid a modified pack: %d %s", r.code, r.err)
	}
	// pinned without --accept fails; with --accept it re-pins.
	if r := run(t, dir, "trust", "--level", "pinned", "vend"); r.code != ExitDrift {
		t.Errorf("trust without accept: %d", r.code)
	}
	mustOK(t, run(t, dir, "trust", "--level", "pinned", "--accept", "vend"))
	mustOK(t, run(t, dir, "doctor"))

	// A missing pack is restored at the locked commit, and verified.
	// The tampered content is part of the lock now, so restore must refuse it.
	if err := os.RemoveAll(filepath.Join(dir, "packs", "vend")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "sync", "--frozen"); r.code != ExitDrift {
		t.Errorf("frozen with a missing pack: %d", r.code)
	}
	r = run(t, dir, "sync")
	if r.code != ExitDrift || !strings.Contains(r.err, "does not match the lock") {
		t.Fatalf("restore of changed content: %d %s", r.code, r.err)
	}
	if pack := filepath.Join(dir, "packs", "vend"); fileExists(pack) {
		t.Error("a failed restore left files behind")
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestGitRestoreAndOrphans(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, gitSkills())
	mustOK(t, run(t, dir, "add", "--as", "vend", "--trust", "pinned", url))
	want := tree(t, filepath.Join(dir, "packs", "vend"))

	if err := os.RemoveAll(filepath.Join(dir, "packs", "vend")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "missing-pack") {
		t.Fatalf("doctor: %d\n%s", r.code, r.out)
	}
	mustOK(t, run(t, dir, "sync"))
	if got := tree(t, filepath.Join(dir, "packs", "vend")); !equalMaps(want, got) {
		t.Error("restored pack differs from the original")
	}
	mustOK(t, run(t, dir, "sync", "--frozen"))
	mustOK(t, run(t, dir, "doctor"))

	// Orphan directory, orphan lock entry, and a crash tombstone.
	write(t, dir, "packs/stray/x/SKILL.md", skillMD("x", "X."))
	write(t, dir, ".removed/.swap-old-1/f", "x")
	var lk lock.File
	json.Unmarshal([]byte(read(t, dir, lock.FileName)), &lk)
	lk.Packs["ghost"] = lk.Packs["vend"]
	b, _ := lk.Marshal()
	write(t, dir, lock.FileName, string(b))
	write(t, dir, ".import/leftover/f", "x")
	r := run(t, dir, "doctor")
	for _, code := range []string{"orphan-pack", "orphan-lock", "leftover-tombstone", "leftover-import"} {
		if !strings.Contains(r.out, code) {
			t.Errorf("doctor lacks %s:\n%s", code, r.out)
		}
	}
	if r.code != ExitDrift {
		t.Errorf("doctor exit %d", r.code)
	}
	if r := run(t, dir, "sync", "--frozen"); r.code != ExitDrift {
		t.Errorf("frozen exit %d", r.code)
	}
	mustOK(t, run(t, dir, "sync"))
	mustOK(t, run(t, dir, "doctor"))
	if !fileExists(filepath.Join(dir, ".removed", "stray", "x", "SKILL.md")) {
		t.Error("orphan was not quarantined")
	}
	if fileExists(filepath.Join(dir, "packs", "stray")) || fileExists(filepath.Join(dir, ".removed", ".swap-old-1")) {
		t.Error("orphan or tombstone still there")
	}
}

func TestRemoveAndPurge(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, gitSkills())
	mustOK(t, run(t, dir, "add", "--as", "vend", url))
	mustOK(t, run(t, dir, "add", "--as", "keep", url))
	// A profile that names the pack is cleaned.
	m := read(t, dir, "skillmeister.yaml")
	write(t, dir, "skillmeister.yaml", strings.Replace(m, "all: [own]", "all: [own, vend]", 1))

	r := mustOK(t, run(t, dir, "remove", "vend"))
	if !strings.Contains(r.out, "moved to .removed/vend") || !strings.Contains(r.out, "profiles: all") {
		t.Errorf("remove output:\n%s", r.out)
	}
	if !fileExists(filepath.Join(dir, ".removed", "vend", "pdf", "SKILL.md")) || fileExists(filepath.Join(dir, "packs", "vend")) {
		t.Error("pack was not quarantined")
	}
	if strings.Contains(read(t, dir, "skillmeister.yaml"), "vend") || strings.Contains(read(t, dir, lock.FileName), "vend") {
		t.Error("manifest or lock still names the pack")
	}
	mustOK(t, run(t, dir, "doctor"))
	mustOK(t, run(t, dir, "sync", "--frozen"))

	// --purge deletes exactly the pack directory.
	before := tree(t, dir, "skillmeister.yaml", lock.FileName, lock.LockName)
	mustOK(t, run(t, dir, "remove", "--purge", "keep"))
	after := tree(t, dir, "skillmeister.yaml", lock.FileName, lock.LockName)
	for k := range before {
		if _, ok := after[k]; !ok && !strings.HasPrefix(k, "packs/keep") {
			t.Errorf("purge deleted %s", k)
		}
	}
	for k := range after {
		if strings.HasPrefix(k, "packs/keep") {
			t.Errorf("purge left %s", k)
		}
	}
	mustOK(t, run(t, dir, "doctor"))

	for _, bad := range [][]string{{"remove", "own"}, {"remove", "nope"}, {"remove"}} {
		if r := run(t, dir, bad...); r.code != ExitUsage {
			t.Errorf("%v: exit %d", bad, r.code)
		}
	}
	// A second quarantine of the same name does not overwrite the first.
	mustOK(t, run(t, dir, "add", "--as", "vend", url))
	mustOK(t, run(t, dir, "remove", "vend"))
	entries, _ := os.ReadDir(filepath.Join(dir, ".removed"))
	if len(entries) != 2 {
		t.Errorf(".removed has %d entries, want 2", len(entries))
	}
}

func TestTrustTransitions(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, gitSkills())
	mustOK(t, run(t, dir, "add", "--as", "vend", url))

	// pinned is not valid for local packs.
	if r := run(t, dir, "trust", "--level", "pinned", "own"); r.code != ExitUsage {
		t.Errorf("pinned on local: %d", r.code)
	}
	for _, bad := range [][]string{{"trust", "vend"}, {"trust", "--level", "modified", "vend"}, {"trust", "--level", "pinned", "nope"}, {"trust", "--level", "pinned"}} {
		if r := run(t, dir, bad...); r.code != ExitUsage {
			t.Errorf("%v: exit %d", bad, r.code)
		}
	}
	// trusted on a fetched pack needs confirmation: --yes when not a TTY.
	if r := run(t, dir, "trust", "--level", "trusted", "vend"); r.code != ExitUsage || !strings.Contains(r.err, "--yes") {
		t.Errorf("no tty, no --yes: %d %s", r.code, r.err)
	}
	// With a TTY, a no answer declines.
	tty := func(answer string, args ...string) result {
		var out, errb strings.Builder
		env := Env{Stdin: strings.NewReader(answer), Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, StdinIsTTY: true}
		code := Run(append([]string{"--library", dir}, args...), env)
		return result{out.String(), errb.String(), code}
	}
	if r := tty("n\n", "trust", "--level", "trusted", "vend"); r.code != ExitUsage || !strings.Contains(r.err, "declined") {
		t.Errorf("decline: %d %s", r.code, r.err)
	}
	mustOK(t, tty("y\n", "trust", "--level", "trusted", "vend"))
	if !strings.Contains(read(t, dir, "skillmeister.yaml"), "  vend:") || !strings.Contains(read(t, dir, "skillmeister.yaml"), "trust: trusted\n    enabled: true\n") {
		t.Errorf("manifest:\n%s", read(t, dir, "skillmeister.yaml"))
	}
	// The lock entry stays, so pinned can verify it again.
	mustOK(t, run(t, dir, "trust", "--level", "pinned", "vend"))
	if r := mustOK(t, run(t, dir, "trust", "--level", "pinned", "vend")); !strings.Contains(r.out, "already pinned") {
		t.Errorf("second pin: %s", r.out)
	}
	// trusted on a local pack needs no confirmation.
	mustOK(t, run(t, dir, "trust", "--level", "trusted", "own"))
}

func TestWriterLockContention(t *testing.T) {
	dir := newLib(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	w, err := lock.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"disable", "own/alpha"}, {"enable", "own"}, {"sync"}, {"remove", "own"}, {"trust", "--level", "trusted", "own"}, {"add", "--as", "x", t.TempDir()}} {
		r := run(t, dir, args...)
		if r.code != ExitIO || !strings.Contains(r.err, "lock") {
			t.Errorf("%v: exit %d %q, want 3 with a lock message", args, r.code, r.err)
		}
	}
	// Readers are not blocked by a writer.
	mustOK(t, run(t, dir, "find", "alpha"))
	mustOK(t, run(t, dir, "doctor"))
	w.Release()
	mustOK(t, run(t, dir, "disable", "own/alpha"))
}

// TestConcurrentWritersKeepManifestValid runs many writers at once. Some lose
// the lock and exit 3; the manifest must stay valid and each success must land.
func TestConcurrentWritersKeepManifestValid(t *testing.T) {
	dir := newLib(t)
	for i := range 6 {
		write(t, dir, fmt.Sprintf("skills/s%d/SKILL.md", i), skillMD(fmt.Sprintf("s%d", i), "S."))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := map[string]bool{}
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			skill := fmt.Sprintf("own/s%d", i)
			for attempt := 0; attempt < 200; attempt++ {
				r := run(t, dir, "disable", skill)
				if r.code == 0 {
					mu.Lock()
					won[skill] = true
					mu.Unlock()
					return
				}
				if r.code != ExitIO {
					t.Errorf("%s: exit %d %s", skill, r.code, r.err)
					return
				}
			}
			t.Errorf("%s never got the lock", skill)
		}()
	}
	wg.Wait()
	if len(won) != 6 {
		t.Fatalf("only %d writers succeeded", len(won))
	}
	r := mustOK(t, run(t, dir, "list", "--json"))
	for _, x := range parseLines(t, r.out) {
		if strings.HasPrefix(x.ID, "own/s") {
			t.Errorf("%s was lost", x.ID)
		}
	}
	m := read(t, dir, "skillmeister.yaml")
	if !strings.Contains(m, "# my own skills") || strings.Count(m, "disable:") != 1 {
		t.Errorf("manifest corrupted:\n%s", m)
	}
	if fileExists(filepath.Join(dir, "skillmeister.yaml.tmp")) {
		t.Error("temp file left behind")
	}
}

func TestDoctorFindings(t *testing.T) {
	dir := newLib(t)
	write(t, dir, "skills/Bad_Name/SKILL.md", skillMD("x", "Bad."))
	write(t, dir, "skills/broken/SKILL.md", "---\nname: [oops\n---\n")
	write(t, dir, "skills/nofront/SKILL.md", "# just text\n")
	write(t, dir, "skills/mismatch/SKILL.md", skillMD("other-name", "Mismatch."))
	write(t, dir, "skills/native/SKILL.md", "---\nname: native\ndescription: N.\nallowed-tools: Bash\n---\nrun !`date`\n")
	write(t, dir, "skills/group/inner/SKILL.md", skillMD("inner", "Nested."))
	write(t, dir, "skills/alpha/deep/SKILL.md", skillMD("deep", "Nested in a skill."))
	r := run(t, dir, "doctor", "--json")
	if r.code != ExitDrift {
		t.Fatalf("exit %d, want 2 (a bad skill name is an error)", r.code)
	}
	var rep struct {
		OK       bool `json:"ok"`
		Findings []struct{ Severity, Code, Subject string }
	}
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, r.out)
	}
	got := map[string]string{}
	for _, f := range rep.Findings {
		got[f.Code+" "+f.Subject] = f.Severity
	}
	for _, want := range []string{
		"skill-name own/Bad_Name", "unparseable own/broken", "no-frontmatter own/nofront", "name-mismatch own/mismatch",
		"native-fields own/native", "nested-skill own/group", "nested-skill own/alpha",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing finding %q in %v", want, got)
		}
	}
	if got["no-frontmatter own/nofront"] != "warn" || got["skill-name own/Bad_Name"] != "error" || rep.OK {
		t.Errorf("severities wrong: %v ok=%v", got, rep.OK)
	}
}

func TestDoctorCleanLibrary(t *testing.T) {
	dir := newLib(t)
	r := mustOK(t, run(t, dir, "doctor"))
	if !strings.Contains(r.out, "no findings") || !strings.Contains(r.out, "own") {
		t.Errorf("doctor:\n%s", r.out)
	}
	r = mustOK(t, run(t, dir, "doctor", "--json"))
	var rep map[string]any
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil || rep["ok"] != true || rep["version"] == "" {
		t.Errorf("json: %v %s", err, r.out)
	}
	if f, ok := rep["findings"].([]any); !ok || len(f) != 0 {
		t.Errorf("findings must be an empty array: %v", rep["findings"])
	}
}

// fakeSkills installs a stand-in for the pinned `skills` CLI. It writes the
// skills and the lock file that the real CLI writes, and logs its arguments.
func fakeSkills(t *testing.T, lib, commit string, names ...string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "args.log")
	var creates, entries []string
	for _, n := range names {
		creates = append(creates, fmt.Sprintf("mkdir -p .claude/skills/%s && printf -- '---\\nname: %s\\ndescription: Fake %s.\\n---\\nbody\\n' > .claude/skills/%s/SKILL.md", n, n, n, n))
		entries = append(entries, fmt.Sprintf(`"%s":{"source":"o/r","commit":"%s","sourceType":"github","installMode":"copy"}`, n, commit))
	}
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\n" + strings.Join(creates, "\n") +
		"\nprintf '{\"version\":2,\"skills\":{" + strings.ReplaceAll(strings.Join(entries, ","), `"`, `\"`) + "}}' > skills-lock.json\n"
	bin := filepath.Join(lib, "node_modules", ".bin", "skills")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestNPXPackLifecycle(t *testing.T) {
	dir := newLib(t)
	commit := strings.Repeat("ab", 20)
	log := fakeSkills(t, dir, commit, "pdf", "docx")
	// An older entry from another source must survive the merge.
	write(t, dir, "skills-lock.json", `{"version":2,"skills":{"other":{"source":"x/y","commit":"1111111"}}}`)

	r := mustOK(t, run(t, dir, "add", "--as", "anth", "--ref", "v1", "o/r"))
	if !strings.Contains(r.out, "added pack anth (npx, 2 skills") || !strings.Contains(r.out, "resolved abababababab") {
		t.Errorf("summary:\n%s", r.out)
	}
	logged, _ := os.ReadFile(log)
	if !strings.Contains(string(logged), "add o/r#v1 --skill * -a claude-code -y --copy") {
		t.Errorf("CLI arguments: %s", logged)
	}
	sl := read(t, dir, "skills-lock.json")
	if !strings.Contains(sl, `"other"`) || !strings.Contains(sl, `"pdf"`) || !strings.Contains(sl, `"docx"`) {
		t.Errorf("skills-lock.json was not merged:\n%s", sl)
	}
	var lk lock.File
	json.Unmarshal([]byte(read(t, dir, lock.FileName)), &lk)
	if lk.Packs["anth"].Resolved != commit || lk.Packs["anth"].Fetch != "npx" {
		t.Errorf("lock: %+v", lk.Packs["anth"])
	}
	m := read(t, dir, "skillmeister.yaml")
	if !strings.Contains(m, "source: o/r") || !strings.Contains(m, "ref: v1") {
		t.Errorf("manifest:\n%s", m)
	}
	mustOK(t, run(t, dir, "doctor"))

	// Restore runs the CLI again, at the locked commit.
	os.RemoveAll(filepath.Join(dir, "packs", "anth"))
	mustOK(t, run(t, dir, "sync"))
	logged, _ = os.ReadFile(log)
	if !strings.Contains(string(logged), "add o/r#"+commit+" ") {
		t.Errorf("restore did not use the locked commit: %s", logged)
	}
	mustOK(t, run(t, dir, "sync", "--frozen"))

	// remove drops this source from skills-lock.json and keeps the other one.
	mustOK(t, run(t, dir, "remove", "anth"))
	sl = read(t, dir, "skills-lock.json")
	if strings.Contains(sl, `"pdf"`) || !strings.Contains(sl, `"other"`) {
		t.Errorf("skills-lock.json after remove:\n%s", sl)
	}
}

func TestNPXNeedsPinnedCLI(t *testing.T) {
	dir := newLib(t)
	r := run(t, dir, "add", "--as", "anth", "o/r")
	if r.code == 0 || !strings.Contains(r.err, "npm ci") {
		t.Errorf("exit %d: %s", r.code, r.err)
	}
	if fileExists(filepath.Join(dir, "packs", "anth")) || strings.Contains(read(t, dir, "skillmeister.yaml"), "anth") {
		t.Error("a failed add changed the library")
	}
}

func TestAddWithSubdirAndRef(t *testing.T) {
	dir := newLib(t)
	url, head := gitRepo(t, map[string]string{"deep/dir/a/SKILL.md": skillMD("a", "A.")})
	mustOK(t, run(t, dir, "add", "--as", "deep", "--path", "deep/dir", "--ref", "main", url))
	if got := strings.Join(ids(t, dir, "--pack", "deep"), ","); got != "deep/a" {
		t.Errorf("ids = %s", got)
	}
	m := read(t, dir, "skillmeister.yaml")
	if !strings.Contains(m, "path: deep/dir") || !strings.Contains(m, "ref: main") {
		t.Errorf("manifest:\n%s", m)
	}
	var lk lock.File
	json.Unmarshal([]byte(read(t, dir, lock.FileName)), &lk)
	if lk.Packs["deep"].Resolved != head {
		t.Errorf("resolved %s, want %s", lk.Packs["deep"].Resolved, head)
	}
	// Restore uses the path hint from the manifest.
	os.RemoveAll(filepath.Join(dir, "packs", "deep"))
	mustOK(t, run(t, dir, "sync"))
	mustOK(t, run(t, dir, "sync", "--frozen"))
	// A repo with no skills at the path is rejected, and nothing is left behind.
	if r := run(t, dir, "add", "--as", "nope", "--path", "missing", url); r.code == 0 {
		t.Error("a missing path must fail")
	}
}
