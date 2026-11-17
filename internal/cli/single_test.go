package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// singleRepo is a repo whose root is one skill, with a nested skills/ dir.
func singleRepo() map[string]string {
	return map[string]string{
		"SKILL.md":                 skillMD("Video_Use", "Edit video by conversation."),
		"helpers/render.py":        "print(1)\n",
		"skills/manim/SKILL.md":    skillMD("manim", "Math animation."),
		".env.example":             "KEY=\n",
		"docs/notes.md":            "notes\n",
		"skills/manim/assets/a.md": "a\n",
	}
}

func TestAddGitSingleSkillRoot(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, singleRepo())
	r := mustOK(t, run(t, dir, "add", "--as", "vu", url))
	if !strings.Contains(r.out, "skills/ also has 1 skills") || !strings.Contains(r.out, "--path skills") {
		t.Errorf("no note about skills/:\n%s", r.out)
	}
	if got := strings.Join(ids(t, dir, "--pack", "vu"), ","); got != "vu/video-use" {
		t.Fatalf("ids = %s", got)
	}
	skill := filepath.Join(dir, "packs", "vu", "video-use")
	for _, p := range []string{"helpers/render.py", ".env.example", "skills/manim/SKILL.md"} {
		if !fileExists(filepath.Join(skill, p)) {
			t.Errorf("%s is missing", p)
		}
	}
	if fileExists(filepath.Join(skill, ".git")) {
		t.Error(".git must not land in the pack")
	}
	if m := read(t, dir, "skillmeister.yaml"); !strings.Contains(m, "path: .\n") {
		t.Errorf("the root must be recorded as path: .\n%s", m)
	}
	// Restore uses the recorded path and gives the same tree.
	os.RemoveAll(filepath.Join(dir, "packs", "vu"))
	mustOK(t, run(t, dir, "sync"))
	mustOK(t, run(t, dir, "sync", "--frozen"))
	// --path names one nested skill as a single skill.
	mustOK(t, run(t, dir, "add", "--as", "manim", "--path", "skills/manim", url))
	if got := strings.Join(ids(t, dir, "--pack", "manim"), ","); got != "manim/manim" {
		t.Errorf("ids = %s", got)
	}
}

func TestAddGitForcedReaddToRoot(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, singleRepo())
	mustOK(t, run(t, dir, "add", "--as", "vu", "--path", "skills", url))
	if got := strings.Join(ids(t, dir, "--pack", "vu"), ","); got != "vu/manim" {
		t.Fatalf("ids = %s", got)
	}
	mustOK(t, run(t, dir, "add", "--force", "--as", "vu", "--path", ".", url))
	if got := strings.Join(ids(t, dir, "--pack", "vu"), ","); got != "vu/video-use" {
		t.Fatalf("ids = %s", got)
	}
	if m := read(t, dir, "skillmeister.yaml"); !strings.Contains(m, "url: "+url+"\n    path: .\n") {
		t.Errorf("the forced re-add must record path: .\n%s", m)
	}
	os.RemoveAll(filepath.Join(dir, "packs", "vu"))
	mustOK(t, run(t, dir, "sync"))
	mustOK(t, run(t, dir, "sync", "--frozen"))
}

func TestAddGitRootIndexStaysMulti(t *testing.T) {
	// A root SKILL.md next to skill directories stays a multi-skill pack, as it
	// was before single-skill support.
	dir := newLib(t)
	url, _ := gitRepo(t, map[string]string{
		"SKILL.md":   skillMD("index", "An index."),
		"a/SKILL.md": skillMD("a", "A."),
		"b/SKILL.md": skillMD("b", "B."),
	})
	mustOK(t, run(t, dir, "add", "--as", "multi", url))
	if got := strings.Join(ids(t, dir, "--pack", "multi"), ","); got != "multi/a,multi/b" {
		t.Errorf("ids = %s", got)
	}
}

func TestAddGitSingleNameFallback(t *testing.T) {
	dir := newLib(t)
	url, _ := gitRepo(t, map[string]string{"SKILL.md": "no frontmatter\n"})
	mustOK(t, run(t, dir, "add", "--as", "plain", url))
	if got := strings.Join(ids(t, dir, "--pack", "plain"), ","); got != "plain/plain" {
		t.Errorf("ids = %s", got)
	}
}

func TestAddLocalSingleSkill(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	for p, c := range singleRepo() {
		write(t, src, p, c)
	}
	// Local setup and secrets are left out; a root symlink inside the skill is kept.
	write(t, src, ".env", "KEY=secret\n")
	write(t, src, ".env.local", "KEY=secret\n")
	write(t, src, ".venv/pyvenv.cfg", "home = /usr\n")
	write(t, src, "node_modules/x/index.js", "1\n")
	write(t, src, "helpers/__pycache__/render.pyc", "x")
	if err := os.Symlink("/usr/bin/python3", filepath.Join(src, ".venv", "python")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs/notes.md", filepath.Join(src, "NOTES.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../docs/notes.md", filepath.Join(src, "helpers", "notes.md")); err != nil {
		t.Fatal(err)
	}
	r := mustOK(t, run(t, dir, "add", "--as", "vu", "--trust", "trusted", src))
	if !strings.Contains(r.out, "not copied (local setup or secrets)") || !strings.Contains(r.out, ".env.local") {
		t.Errorf("no note about skipped files:\n%s", r.out)
	}
	if got := strings.Join(ids(t, dir, "--pack", "vu"), ","); got != "vu/video-use" {
		t.Fatalf("ids = %s", got)
	}
	skill := filepath.Join(dir, "packs", "vu", "video-use")
	for _, p := range []string{".env", ".env.local", ".venv", "node_modules", "helpers/__pycache__"} {
		if fileExists(filepath.Join(skill, p)) {
			t.Errorf("%s must not be copied", p)
		}
	}
	for _, p := range []string{".env.example", "NOTES.md", "helpers/notes.md", "helpers/render.py"} {
		if _, err := os.Lstat(filepath.Join(skill, p)); err != nil {
			t.Errorf("%s is missing: %v", p, err)
		}
	}
	// A root symlink that leaves the source is still rejected.
	bad := t.TempDir()
	write(t, bad, "SKILL.md", skillMD("bad", "Bad."))
	if err := os.Symlink("../outside", filepath.Join(bad, "out")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, "add", "--as", "bad", bad); r.code == 0 || !strings.Contains(r.err, "leaves the skill directory") {
		t.Errorf("an escaping link must fail: %d %s", r.code, r.err)
	}
}

func TestAddGitSymlinkedSinglePathIsRejected(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "real/SKILL.md", skillMD("real", "Real."))
	os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	if err := os.Symlink("../../real", filepath.Join(src, "a", "b", "link")); err != nil {
		t.Fatal(err)
	}
	url := commitAll(t, src)
	r := run(t, dir, "add", "--as", "r2", "--path", "a/b/link", url)
	if r.code == 0 || !strings.Contains(r.err, "symlink") {
		t.Fatalf("a symlinked single skill must fail: %d %s %s", r.code, r.out, r.err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "packs", "r2")); err == nil {
		t.Error("the failed add left a pack")
	}
}

func TestAddGitSingleNotesIgnoredFilesAndDropsAttributes(t *testing.T) {
	dir := newLib(t)
	files := singleRepo()
	files[".env.development"] = "K=v\n"
	files["helpers/a.pyc"] = "x"
	files[".gitattributes"] = "* text=auto eol=crlf\n"
	url, _ := gitRepo(t, files)
	r := mustOK(t, run(t, dir, "add", "--as", "vu", url))
	if !strings.Contains(r.out, "leaves out these fetched files: video-use/.env.development, video-use/helpers/a.pyc") {
		t.Errorf("no note about ignored files:\n%s", r.out)
	}
	if fileExists(filepath.Join(dir, "packs", "vu", "video-use", ".gitattributes")) {
		t.Error(".gitattributes of the source must not land in the pack")
	}
}

func TestAddGitForcedReaddFromEmptyPath(t *testing.T) {
	// A hand-written manifest has no path. A forced re-add records ".".
	dir := newLib(t)
	url, _ := gitRepo(t, singleRepo())
	mustOK(t, run(t, dir, "add", "--as", "vu", url))
	m := read(t, dir, "skillmeister.yaml")
	write(t, dir, "skillmeister.yaml", strings.Replace(m, "    path: .\n", "", 1))
	r := mustOK(t, run(t, dir, "add", "--force", "--as", "vu", url))
	if !strings.Contains(r.out, "skills/ also has 1 skills") {
		t.Errorf("a forced re-add must print the notes too:\n%s", r.out)
	}
	m = read(t, dir, "skillmeister.yaml")
	if vu := m[strings.Index(m, "  vu:"):]; !strings.Contains(vu, "    path: .\n") {
		t.Errorf("manifest:\n%s", m)
	}
}

func TestUpdateSingleSkillRoot(t *testing.T) {
	dir := newLib(t)
	src := t.TempDir()
	for p, c := range singleRepo() {
		write(t, src, p, c)
	}
	url := commitAll(t, src)
	mustOK(t, run(t, dir, "add", "--as", "vu", url))
	write(t, src, "SKILL.md", skillMD("video-use", "Version two."))
	commitHead(t, src)
	r := mustOK(t, run(t, dir, "update", "--apply", "--yes", "vu"))
	if !strings.Contains(r.out, "updated vu") {
		t.Errorf("update:\n%s", r.out)
	}
	if !strings.Contains(read(t, dir, "packs/vu/video-use/SKILL.md"), "Version two.") {
		t.Error("the skill was not updated")
	}
	mustOK(t, run(t, dir, "sync", "--frozen"))
}

func TestAddLocalMultiLinkToSiblingDir(t *testing.T) {
	// A link from a subdirectory to another directory of the same skill is
	// kept; the check is against the skill directory, not the subdirectory.
	dir := newLib(t)
	src := t.TempDir()
	write(t, src, "s/SKILL.md", skillMD("s", "S."))
	write(t, src, "s/shared/x.md", "x\n")
	os.MkdirAll(filepath.Join(src, "s", "refs"), 0o755)
	if err := os.Symlink("../shared/x.md", filepath.Join(src, "s", "refs", "x.md")); err != nil {
		t.Fatal(err)
	}
	mustOK(t, run(t, dir, "add", "--as", "m", src))
	if got := read(t, dir, "packs/m/s/refs/x.md"); got != "x\n" {
		t.Errorf("link content = %q", got)
	}
}
