package pack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds every subprocess.
const commandTimeout = 10 * time.Minute

// run runs a command in dir and returns its stdout. On failure the error holds
// the last lines of stderr. Prompts are disabled: a command never waits for input.
func run(dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GIT_TERMINAL_PROMPT=0", "CI=1"}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if lines := strings.Split(tail, "\n"); len(lines) > 8 {
			tail = strings.Join(lines[len(lines)-8:], "\n")
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
