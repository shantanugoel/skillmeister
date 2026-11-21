package pack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// commandTimeout bounds every subprocess.
const commandTimeout = 10 * time.Minute

// run runs a command in dir and returns its stdout. On failure the error holds
// the last lines of stderr. Prompts are disabled: a command never waits for input.
func run(dir string, env []string, name string, args ...string) (string, error) {
	return runCmd(dir, env, false, name, args...)
}

// ansiRE matches the terminal color codes that a CLI prints.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// runCmd is run. With withStdout, the error also holds the last lines of
// stdout, for a CLI that prints its errors there.
func runCmd(dir string, env []string, withStdout bool, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GIT_TERMINAL_PROMPT=0", "CI=1", "GIT_ALLOW_PROTOCOL=file:git:http:https:ssh"}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		tail := lastLines(stderr.String(), 8)
		if withStdout {
			if out := lastLines(cleanCLIOutput(stdout.String()), 12); out != "" {
				tail = strings.TrimSpace(tail + "\n" + out)
			}
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s: timed out after %s", name, args[0], commandTimeout)
		}
		return "", fmt.Errorf("%s %s: %w\n%s", name, args[0], err, tail)
	}
	return stdout.String(), nil
}

// checkArg rejects values that a command line parser could read as an option.
func checkArg(what, v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s %q must not start with a dash", what, v)
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("%s contains a control character", what)
	}
	return nil
}

// lastLines returns the last n non-empty lines of s.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \r\t"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// cleanCLIOutput makes the terminal output of a CLI readable in an error. A
// spinner redraws its line after a cursor move to column 1 (ESC[1G) or a
// carriage return, so only the text after the last redraw stays. Color codes
// and the empty gutter lines of the `skills` CLI are removed.
func cleanCLIOutput(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.LastIndex(line, "\x1b[1G"); i >= 0 {
			line = line[i+len("\x1b[1G"):]
		}
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		line = strings.TrimSpace(ansiRE.ReplaceAllString(line, ""))
		if line == "" || line == "│" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
