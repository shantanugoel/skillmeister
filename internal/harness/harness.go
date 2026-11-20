// Package harness renders the shim and checks that the agent harnesses see it.
package harness

import (
	_ "embed"
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

// Template is the shim template. The binary carries it, so a library made by
// `init` needs no copy and a new binary brings a new template.
//
//go:embed SKILL.md.tmpl
var Template []byte

// Finding codes of the harness checks.
const (
	CodeShimMissing = "shim-missing"
	CodeShimStale   = "shim-stale"
	CodeShimBinary  = "shim-binary"
	CodeShimRoot    = "shim-root"
)

// Render fills the shim template with the absolute binary and library paths.
// The paths sit inside double quotes in the shim's shell commands, so they
// cannot hold characters that break or expand inside double quotes.
func Render(tmpl []byte, bin, root string) ([]byte, error) {
	for _, p := range []string{bin, root} {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("path %q is not absolute", p)
		}
		if strings.ContainsAny(p, "\"\\$`\n\r") {
			return nil, fmt.Errorf("path %q contains a character that is not safe inside the shim's quoted command line", p)
		}
	}
	return []byte(strings.NewReplacer("{{BIN}}", bin, "{{ROOT}}", root).Replace(string(tmpl))), nil
}

// ShimPaths lists where the rendered shim lives. Both discovery paths hold the
// same file, and each harness lists the skill once.
func ShimPaths(home string) []string {
	return []string{
		filepath.Join(home, ".agents", "skills", "skillmeister", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "skillmeister", "SKILL.md"),
	}
}

// cmdRE finds the find command of a rendered shim: binary, then library.
var cmdRE = regexp.MustCompile(`(?m)^"([^"\n]+)" --library "([^"\n]+)" find --json`)

// Check inspects the installed shims. An empty home skips the checks.
func Check(home string, lib *library.Library) []doctor.Finding {
	if home == "" {
		return nil
	}
	var out []doctor.Finding
	add := func(code, subject, format string, args ...any) {
		out = append(out, doctor.Finding{Severity: doctor.Error, Code: code, Subject: subject, Message: fmt.Sprintf(format, args...)})
	}
	const fix = "run `skillmeister init` again"
	tmpl := Template
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
	return out
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
