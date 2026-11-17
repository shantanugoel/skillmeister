package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cloneTree copies the repo files (tracked, plus new files that are not
// ignored) into a fresh directory. It stands in for a fresh clone.
func cloneTree(t *testing.T) string {
	t.Helper()
	needGit(t)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		src := filepath.Join(repo, rel)
		info, err := os.Lstat(src)
		if err != nil || info.IsDir() {
			continue // a deleted file that git still lists
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(src)
			os.Symlink(target, filepath.Join(dst, rel))
			continue
		}
		in, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		o, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(o, in)
		in.Close()
		o.Close()
	}
	return dst
}

func installEnv(home, bin string) []string {
	// The script must set GOFLAGS and GOTOOLCHAIN itself. GOPROXY=off proves
	// that nothing needs the network.
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOFLAGS=") && !strings.HasPrefix(kv, "GOTOOLCHAIN=") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home, "BIN_DIR="+bin, "GOPROXY=off")
}

func runInstall(t *testing.T, clone, home, bin string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(clone, "scripts", "install.sh"))
	cmd.Env = installEnv(home, bin)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// TestInstallScript runs install.sh from a fresh copy of the repo, with a fresh
// HOME and no network.
func TestInstallScript(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	clone := cloneTree(t)
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")

	out, err := runInstall(t, clone, home, bin)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	shims := []string{
		filepath.Join(home, ".agents", "skills", "skillmeister", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "skillmeister", "SKILL.md"),
	}
	first := readAbs(t, shims[0])
	if first != readAbs(t, shims[1]) {
		t.Error("the two shims differ")
	}
	physBin, _ := filepath.EvalSymlinks(bin)
	if !strings.Contains(first, `"`+filepath.Join(physBin, "skillmeister")+`" --library "`) || strings.Contains(first, "{{") {
		t.Errorf("shim is not rendered:\n%s", first)
	}
	// The install does not touch the agent memory files.
	for _, f := range []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".config", "opencode", "AGENTS.md")} {
		if fileExists(f) {
			t.Errorf("install created %s", f)
		}
	}
	if !strings.Contains(out, "no findings") {
		t.Errorf("doctor was not clean:\n%s", out)
	}

	// The exact command line of the installed shim works.
	m := regexpCmd.FindStringSubmatch(first)
	if m == nil {
		t.Fatal("no command line in the shim")
	}
	cmd := exec.Command(m[1], "--library", m[2], "find", "--json", "--limit", "8", "--", "nothing-matches")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("shim command: %v\n%s", err, o)
	}

	// Running again changes nothing.
	before := first
	out, err = runInstall(t, clone, home, bin)
	if err != nil {
		t.Fatalf("second install failed: %v\n%s", err, out)
	}
	if readAbs(t, shims[0]) != before {
		t.Error("shim changed on the second run")
	}
	// doctor catches a hand-edited shim.
	os.WriteFile(shims[0], []byte(first+"\nextra"), 0o644)
	doc := exec.Command(filepath.Join(bin, "skillmeister"), "--library", clone, "doctor")
	doc.Env = append(os.Environ(), "HOME="+home)
	o, err := doc.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != ExitDrift || !strings.Contains(string(o), "shim-stale") {
		t.Errorf("doctor on a stale shim: %v\n%s", err, o)
	}
}

func TestInstallRefusesToClobber(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	clone := cloneTree(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "skills", "skillmeister")
	write(t, dir, "SKILL.md", "---\nname: something-else\n---\nmine\n")
	out, err := runInstall(t, clone, home, filepath.Join(home, "bin"))
	if err == nil || !strings.Contains(out, "not a skillmeister shim") {
		t.Fatalf("install over a real directory: %v\n%s", err, out)
	}
	if read(t, dir, "SKILL.md") != "---\nname: something-else\n---\nmine\n" {
		t.Error("the directory was changed")
	}
	if fileExists(filepath.Join(home, ".agents", "skills", "skillmeister")) {
		t.Error("the other path was written before the refusal")
	}
	// A symlink is refused as well.
	home2 := t.TempDir()
	os.MkdirAll(filepath.Join(home2, ".agents", "skills"), 0o755)
	os.Symlink(t.TempDir(), filepath.Join(home2, ".agents", "skills", "skillmeister"))
	if out, err := runInstall(t, clone, home2, filepath.Join(home2, "bin")); err == nil || !strings.Contains(out, "symlink") {
		t.Errorf("symlink: %v\n%s", err, out)
	}
}

var regexpCmd = regexp.MustCompile(`(?m)^"([^"\n]+)" --library "([^"\n]+)" find --json`)

func readAbs(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestInstallRelativeBinDir checks that a relative BIN_DIR works, and that an
// agent memory file that already exists is left as it is.
func TestInstallRelativeBinDir(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	clone := cloneTree(t)
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude"), "CLAUDE.md", "# My rules\nno trailing newline")
	cmd := exec.Command("sh", filepath.Join(clone, "scripts", "install.sh"))
	cmd.Dir = home
	cmd.Env = installEnv(home, "rel-bin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if got := readAbs(t, filepath.Join(home, ".claude", "CLAUDE.md")); got != "# My rules\nno trailing newline" {
		t.Errorf("CLAUDE.md was changed:\n%q", got)
	}
	if !fileExists(filepath.Join(home, "rel-bin", "skillmeister")) {
		t.Error("the relative BIN_DIR was not used")
	}
}
