package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/lock"
)

// gitRepoWithLinks builds a repo that holds symlinks, which write() cannot make.
func gitRepoWithLinks(t *testing.T, files map[string]string, links map[string]string) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	for p, c := range files {
		write(t, dir, p, c)
	}
	for p, target := range links {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, p)); err != nil {
			t.Skip("symlinks unavailable")
		}
	}
	return commitAll(t, dir)
}

func TestHostileSymlinkedSubtreeCannotTakeOwnSkills(t *testing.T) {
	dir := newLib(t)
	// From .import/<work>/repo/skills, ../../../skills is the library's own skills/.
	url := gitRepoWithLinks(t, map[string]string{"README.md": "x"}, map[string]string{"skills": "../../../skills"})
	before := tree(t, dir, lock.LockName)
	delete(before, ".import/")
	r := run(t, dir, "add", "--as", "evil", url)
	if r.code == 0 {
		t.Fatalf("add succeeded:\n%s", r.out)
	}
	after := tree(t, dir, lock.LockName)
	delete(after, ".import/")
	if !equalMaps(before, after) {
		t.Error("the library changed, or own skills moved")
	}
	for _, f := range []string{"skills/alpha/SKILL.md", "skills/beta/SKILL.md"} {
		if !fileExists(filepath.Join(dir, f)) {
			t.Errorf("%s was taken", f)
		}
	}
	// The same through --path.
	r = run(t, dir, "add", "--as", "evil", "--path", "skills", url)
	if r.code == 0 || !fileExists(filepath.Join(dir, "skills", "alpha", "SKILL.md")) {
		t.Errorf("--path variant: exit %d", r.code)
	}
	// And a link to another pack.
	mustOK(t, run(t, dir, "add", "--as", "victim", newSkillsDir(t)))
	url2 := gitRepoWithLinks(t, map[string]string{"README.md": "x"}, map[string]string{"skills": "../../../packs/victim"})
	if r := run(t, dir, "add", "--as", "thief", url2); r.code == 0 {
		t.Error("a link to another pack was followed")
	}
	if !fileExists(filepath.Join(dir, "packs", "victim", "v", "SKILL.md")) {
		t.Error("the victim pack was taken")
	}
}

func newSkillsDir(t *testing.T) string {
	d := t.TempDir()
	write(t, d, "v/SKILL.md", skillMD("v", "V."))
	return d
}

func TestHostileSymlinksInsideSkillsAreRejected(t *testing.T) {
	dir := newLib(t)
	for name, link := range map[string]string{
		"absolute":   "/etc/passwd",
		"parent":     "../../../../skillmeister.yaml",
		"to sibling": "../../other",
		"far up":     "../../..",
	} {
		t.Run(name, func(t *testing.T) {
			url := gitRepoWithLinks(t,
				map[string]string{"skills/a/SKILL.md": skillMD("a", "A."), "skills/other/SKILL.md": skillMD("other", "O.")},
				map[string]string{"skills/a/ref/link": link})
			r := run(t, dir, "add", "--as", "p", url)
			if r.code == 0 || !strings.Contains(r.err, "symlinks that leave") {
				t.Fatalf("exit %d: %s", r.code, r.err)
			}
			if fileExists(filepath.Join(dir, "packs", "p")) || strings.Contains(read(t, dir, "skillmeister.yaml"), "  p:") {
				t.Error("a rejected pack was installed")
			}
		})
	}
	// A link that stays inside its skill is fine.
	url := gitRepoWithLinks(t,
		map[string]string{"skills/a/SKILL.md": skillMD("a", "A."), "skills/a/docs/real.md": "x"},
		map[string]string{"skills/a/docs/alias.md": "real.md"})
	mustOK(t, run(t, dir, "add", "--as", "ok", url))
	mustOK(t, run(t, dir, "doctor"))
}

func TestDoctorCatchesPostInstallSymlinkEscapes(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, map[string]string{"skills/a/SKILL.md": skillMD("a", "A.")})
	mustOK(t, run(t, dir, "add", "--as", "p", url))
	mustOK(t, run(t, dir, "doctor"))
	mustOK(t, run(t, dir, "sync", "--frozen"))

	// An escaping symlink added inside a skill after install must fail doctor,
	// sync --frozen, and sync.
	leak := filepath.Join(dir, "packs", "p", "a", "leak")
	if err := os.Symlink("/etc/passwd", leak); err != nil {
		t.Skip("symlinks unavailable")
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after escaping symlink: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := run(t, dir, "sync", "--frozen"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("sync --frozen after escaping symlink: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := run(t, dir, "sync"); r.code != ExitDrift || !strings.Contains(r.err, "symlinks that leave") {
		t.Fatalf("sync after escaping symlink: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(leak)
	mustOK(t, run(t, dir, "doctor"))

	// Chained intermediate symlinks that stay inside lexically but escape at
	// runtime must also be caught.
	deepDir := filepath.Join(dir, "packs", "p", "a", "deep", "d1", "d2")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../..", filepath.Join(deepDir, "in")); err != nil {
		t.Fatal(err)
	}
	chain := filepath.Join(dir, "packs", "p", "a", "chain")
	if err := os.Symlink("deep/d1/d2/in/../../..", chain); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after chained symlink escape: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(chain)
	os.RemoveAll(filepath.Join(dir, "packs", "p", "a", "deep"))
	mustOK(t, run(t, dir, "doctor"))

	// A skill directory that is itself a symlink is also caught.
	symSkill := filepath.Join(dir, "packs", "p", "linked")
	if err := os.Symlink(filepath.Join(dir, "skills", "alpha"), symSkill); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after symlinked skill dir: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(symSkill)

	// A pack directory replaced with a symlink is caught as symlink-escape.
	packDir := filepath.Join(dir, "packs", "p")
	backup := filepath.Join(t.TempDir(), "p")
	if err := os.Rename(packDir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backup, packDir); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after symlinked pack dir: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(packDir)
	if err := os.Rename(backup, packDir); err != nil {
		t.Fatal(err)
	}

	// own: hidden and non-skill directories are left alone, while symlinks
	// inside discovered skills and symlinked skill dirs are caught.
	write(t, dir, "skills/.hidden/note.txt", "ok")
	write(t, dir, "skills/notes/readme.txt", "not a skill")
	mustOK(t, run(t, dir, "doctor"))

	ownLeak := filepath.Join(dir, "skills", "alpha", "leak")
	if err := os.Symlink("/etc/passwd", ownLeak); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after own skill escaping symlink: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(ownLeak)

	ownSym := filepath.Join(dir, "skills", "linked")
	if err := os.Symlink(filepath.Join(dir, "skills", "alpha"), ownSym); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "doctor"); r.code != ExitDrift || !strings.Contains(r.out, "symlink-escape") {
		t.Fatalf("doctor after own symlinked skill dir: exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	os.Remove(ownSym)
	mustOK(t, run(t, dir, "doctor"))

	// An unreadable subdirectory is reported as unreadable-pack, not symlink-escape.
	if os.Getuid() != 0 {
		blocked := filepath.Join(dir, "packs", "p", "a", "blocked")
		if err := os.MkdirAll(blocked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(blocked, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(blocked, 0o755)
		if _, err := os.ReadDir(blocked); err == nil {
			t.Skip("filesystem ignores chmod 000")
		}
		r := run(t, dir, "doctor")
		if r.code != ExitDrift || !strings.Contains(r.out, "unreadable-pack") || strings.Contains(r.out, "symlink-escape") {
			t.Fatalf("doctor on unreadable dir: exit %d\n%s\n%s", r.code, r.out, r.err)
		}
		if r := run(t, dir, "sync", "--frozen"); r.code != ExitDrift || !strings.Contains(r.out, "unreadable-pack") {
			t.Fatalf("sync --frozen on unreadable dir: exit %d\n%s\n%s", r.code, r.out, r.err)
		}
		os.Chmod(blocked, 0o755)
	}
}

func TestPathHintCannotEscapeClone(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, map[string]string{"x/a/SKILL.md": skillMD("a", "A.")})
	r := run(t, dir, "add", "--as", "p", "--path", "../../..", url)
	if r.code == 0 {
		t.Fatalf("add succeeded:\n%s", r.out)
	}
	if !fileExists(filepath.Join(dir, "skills", "alpha", "SKILL.md")) {
		t.Error("own skills were taken")
	}
}

func TestSyncRecoversInterruptedSwap(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "s/SKILL.md", skillMD("s", "Only copy."))
	mustOK(t, run(t, dir, "add", "--as", "loc", src))
	// Simulate a crash between the two renames of a swap.
	if err := os.MkdirAll(filepath.Join(dir, ".removed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "packs", "loc"), filepath.Join(dir, ".removed", ".swap-loc-1700000000000000000")); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, "doctor")
	if r.code != ExitDrift || !strings.Contains(r.out, "only here") {
		t.Fatalf("doctor: %d\n%s", r.code, r.out)
	}
	r = mustOK(t, run(t, dir, "sync"))
	if !strings.Contains(r.out, "recovered pack loc") {
		t.Errorf("sync output:\n%s", r.out)
	}
	if !fileExists(filepath.Join(dir, "packs", "loc", "s", "SKILL.md")) {
		t.Error("the only copy was lost")
	}
	mustOK(t, run(t, dir, "doctor"))
}

func TestForceReplaceRollsBackOnFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "s/SKILL.md", skillMD("s", "Version one."))
	mustOK(t, run(t, dir, "add", "--as", "loc", src))
	write(t, src, "s/SKILL.md", skillMD("s", "Version two."))
	// A read-only library root makes the manifest write fail after the swap.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	r := run(t, dir, "add", "--as", "loc", "--force", src)
	os.Chmod(dir, 0o755)
	if r.code == 0 {
		t.Fatal("the add should have failed")
	}
	if got := read(t, dir, "packs/loc/s/SKILL.md"); !strings.Contains(got, "Version one") {
		t.Errorf("the old pack did not come back:\n%s", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".removed")); len(entries) != 0 {
		t.Errorf("tombstone left: %v", entries)
	}
	mustOK(t, run(t, dir, "doctor"))
}

func TestRemoveKeepsSharedSkillsLockEntries(t *testing.T) {
	dir := newLib(t)
	commit := strings.Repeat("cd", 20)
	_, _ = fakeSkills(t, dir, commit, "pdf")
	mustOK(t, run(t, dir, "add", "--as", "one", "o/r"))
	mustOK(t, run(t, dir, "add", "--as", "two", "--ref", "v2", "o/r"))
	mustOK(t, run(t, dir, "remove", "one"))
	if !strings.Contains(read(t, dir, "skills-lock.json"), `"pdf"`) {
		t.Error("removing one pack dropped the entries of the other pack that shares the source")
	}
	mustOK(t, run(t, dir, "remove", "two"))
	if strings.Contains(read(t, dir, "skills-lock.json"), `"pdf"`) {
		t.Error("the entries stayed after the last pack was removed")
	}
}
