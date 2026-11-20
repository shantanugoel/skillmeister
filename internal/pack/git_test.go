package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shantanugoel/skillmeister/internal/manifest"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

// TestFetchGitFallbackResolvesRefs forces the shallow fetch to fail and checks
// that the full-fetch fallback resolves default HEAD, named branches, tags,
// and non-tip commit SHAs to their exact revisions.
func TestFetchGitFallbackResolvesRefs(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	remote := t.TempDir()
	gitCmd(t, remote, "init", "-q", "-b", "main")
	skillPath := filepath.Join(remote, "skills", "demo", "SKILL.md")

	commitSkill := func(desc string) string {
		writeTestFile(t, skillPath, "---\nname: demo\ndescription: "+desc+"\n---\nBody.\n", 0o644)
		gitCmd(t, remote, "add", ".")
		gitCmd(t, remote, "commit", "-q", "-m", desc)
		return gitCmd(t, remote, "rev-parse", "HEAD")
	}

	oldSHA := commitSkill("old commit")
	tagSHA := commitSkill("tagged release")
	gitCmd(t, remote, "tag", "v1")
	mainSHA := commitSkill("main tip")

	// Create a branch named "alpha" (sorts before "main") on a distinct commit
	// so any buggy FETCH_HEAD fallback would pick alpha instead of main.
	gitCmd(t, remote, "checkout", "-q", "-b", "alpha", oldSHA)
	alphaSHA := commitSkill("alpha branch")
	gitCmd(t, remote, "checkout", "-q", "main")

	// Wrap git in PATH so any `--depth` fetch fails, forcing the fallback path.
	binDir := t.TempDir()
	wrapper := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"--depth\" ]; then\n    echo 'shallow fetch disabled by test' >&2\n    exit 128\n  fi\ndone\nexec \"" + realGit + "\" \"$@\"\n"
	writeTestFile(t, filepath.Join(binDir, "git"), wrapper, 0o755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	libDir := t.TempDir()
	root, err := os.OpenRoot(libDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	cases := []struct {
		name    string
		ref     string
		wantSHA string
	}{
		{"default HEAD", "", mainSHA},
		{"explicit HEAD", "HEAD", mainSHA},
		{"default branch", "main", mainSHA},
		{"other branch", "alpha", alphaSHA},
		{"tag", "v1", tagSHA},
		{"non-tip commit", oldSHA, oldSHA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Fetch(root, Spec{
				Name:  "demo",
				Fetch: manifest.FetchGit,
				URL:   remote,
				Ref:   tc.ref,
			})
			if err != nil {
				t.Fatalf("Fetch(ref=%q): %v", tc.ref, err)
			}
			defer Cleanup(root, s)
			if s.Resolved != tc.wantSHA {
				t.Errorf("resolved = %s, want %s", s.Resolved, tc.wantSHA)
			}
		})
	}
}

// TestFetchGitIgnoresGlobalHooks verifies that a user's global core.hooksPath
// cannot run hooks during fetchGit.
func TestFetchGitIgnoresGlobalHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	remote := t.TempDir()
	gitCmd(t, remote, "init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(remote, "skills", "demo", "SKILL.md"), "---\nname: demo\ndescription: D.\n---\nBody.\n", 0o644)
	gitCmd(t, remote, "add", ".")
	gitCmd(t, remote, "commit", "-q", "-m", "init")

	hooksDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	writeTestFile(t, filepath.Join(hooksDir, "post-checkout"), "#!/bin/sh\necho ran > \""+marker+"\"\n", 0o755)
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	writeTestFile(t, cfg, "[core]\n\thooksPath = "+hooksDir+"\n", 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	libDir := t.TempDir()
	root, err := os.OpenRoot(libDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	s, err := Fetch(root, Spec{Name: "demo", Fetch: manifest.FetchGit, URL: remote})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer Cleanup(root, s)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("global post-checkout hook executed during fetchGit")
	}
}
