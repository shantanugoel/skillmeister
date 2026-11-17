// Package harness renders the shim and checks that the agent harnesses see it.
package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/doctor"
	"github.com/shantanugoel/skillmeister/internal/library"
)

// TemplatePath is the shim template, relative to the library root.
const TemplatePath = "shim/skillmeister/SKILL.md.tmpl"

// Finding codes of the harness checks.
const (
	CodeShimMissing   = "shim-missing"
	CodeShimStale     = "shim-stale"
	CodeShimBinary    = "shim-binary"
	CodeShimRoot      = "shim-root"
	CodeShimTemplate  = "shim-template"
	CodeMemoryMissing = "memory-missing"
)

// Render fills the shim template with the absolute binary and library paths.
// The paths sit inside double quotes in the shim, so they cannot hold a quote
// or a newline.
func Render(tmpl []byte, bin, root string) ([]byte, error) {
	for _, p := range []string{bin, root} {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("path %q is not absolute", p)
		}
		if strings.ContainsAny(p, "\"\n\r") {
			return nil, fmt.Errorf("path %q contains a quote or a newline", p)
		}
	}
	return []byte(strings.NewReplacer("{{BIN}}", bin, "{{ROOT}}", root).Replace(string(tmpl))), nil
}

// MemoryLine is the line that install appends to the agent memory files.
func MemoryLine(root string) string {
	return fmt.Sprintf("Skill library at `%s`, searched with `skillmeister`. Use it only when I explicitly ask for a skill.", root)
}

// ShimPaths lists where the rendered shim lives. Both discovery paths hold the
// same file, and each harness lists the skill once.
func ShimPaths(home string) []string {
	return []string{
		filepath.Join(home, ".agents", "skills", "skillmeister", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "skillmeister", "SKILL.md"),
	}
}

// MemoryFiles lists the memory files that carry the memory line.
func MemoryFiles(home string) []string {
	return []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".config", "opencode", "AGENTS.md"),
	}
}

// cmdRE finds the find command of a rendered shim: binary, then library.
var cmdRE = regexp.MustCompile(`(?m)^"([^"\n]+)" --library "([^"\n]+)" find --json`)

// Check inspects the installed shims and memory lines. An empty home skips the
// checks.
func Check(home string, lib *library.Library) []doctor.Finding {
	if home == "" {
		return nil
	}
	var out []doctor.Finding
	add := func(code, subject, format string, args ...any) {
		out = append(out, doctor.Finding{Severity: doctor.Error, Code: code, Subject: subject, Message: fmt.Sprintf(format, args...)})
	}
	const fix = "run scripts/install.sh again"
	tmpl, err := lib.Root.ReadFile(TemplatePath)
	if err != nil {
		add(CodeShimTemplate, TemplatePath, "cannot read the shim template: %v", err)
		return out
	}
	for _, p := range ShimPaths(home) {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			add(CodeShimMissing, p, "the shim is not installed; %s", fix)
			continue
		}
		if err != nil {
			add(CodeShimStale, p, "cannot read the shim: %v", err)
			continue
		}
		m := cmdRE.FindSubmatch(data)
		if m == nil {
			add(CodeShimStale, p, "the shim has no skillmeister command line; %s", fix)
			continue
		}
		bin, root := string(m[1]), string(m[2])
		if st, err := os.Stat(bin); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			add(CodeShimBinary, p, "the binary %s does not exist or is not executable; %s", bin, fix)
			continue
		}
		if !samePath(root, lib.Abs) {
			add(CodeShimRoot, p, "the shim points to the library %s, not %s; %s", root, lib.Abs, fix)
			continue
		}
		want, err := Render(tmpl, bin, root)
		if err != nil {
			add(CodeShimStale, p, "%v", err)
			continue
		}
		if string(want) != string(data) {
			add(CodeShimStale, p, "the shim differs from the template; %s", fix)
		}
	}
	// install.sh writes the physical path, and the user may name the library
	// through a symlink. Both spellings count.
	lines := []string{MemoryLine(lib.Abs)}
	if r, err := filepath.EvalSymlinks(lib.Abs); err == nil && r != lib.Abs {
		lines = append(lines, MemoryLine(r))
	}
	for _, p := range MemoryFiles(home) {
		data, err := os.ReadFile(p)
		if err != nil || !containsAny(string(data), lines) {
			add(CodeMemoryMissing, p, "the memory line is missing; %s", fix)
		}
	}
	return out
}

func containsAny(text string, lines []string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r")
		for _, want := range lines {
			if l == want {
				return true
			}
		}
	}
	return false
}

// samePath compares two paths after it resolves symlinks.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
