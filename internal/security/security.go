// Package security runs lexical heuristics over the skills of a library. The
// rules find patterns, not proof of harm. They exist to point a reviewer at the
// lines that deserve a look.
package security

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// Rule names.
const (
	RuleScript       = "bundled-script"
	RuleCurlPipe     = "curl-pipe-shell"
	RuleSudo         = "sudo"
	RuleEval         = "eval"
	RuleBase64       = "base64"
	RuleSecretPath   = "secret-path"
	RuleBroadTools   = "broad-allowed-tools"
	RuleBang         = "bang-command"
	RuleParentEscape = "parent-path"
)

// Severity of a hit.
type Severity string

// Severities. A hit in a pack declared `trusted` is a warning, because the user
// accepted that pack. Every other hit is high.
const (
	High Severity = "high"
	Warn Severity = "warn"
)

// Hit is one match.
type Hit struct {
	Severity Severity
	Rule     string
	Pack     string
	Skill    string // pack/skill ID
	File     string // path relative to the skill directory
	Line     int    // 0 when the rule is about the whole file
	Detail   string
}

type lineRule struct {
	name string
	re   *regexp.Regexp
	note string
}

var lineRules = []lineRule{
	{RuleCurlPipe, regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "downloads and runs code in one step"},
	{RuleSudo, regexp.MustCompile(`(^|[^A-Za-z0-9_-])sudo\s`), "asks for root"},
	{RuleEval, regexp.MustCompile(`(^|[^A-Za-z0-9_.-])eval[\s(]+["'$` + "`" + `A-Za-z(]`), "runs a constructed string"},
	{RuleBase64, regexp.MustCompile(`base64\s+(-d|-D|--decode)\b|[A-Za-z0-9+/]{160,}={0,2}`), "decodes or holds an opaque blob"},
	{RuleSecretPath, regexp.MustCompile(`(?i)(\.ssh/|\.aws/|\.gnupg/|\.kube/config|\.docker/config|id_rsa|id_ed25519|/etc/shadow|\.netrc|\.npmrc|\.pypirc|credentials\.json|(^|[\s'"/])\.env\b|keychain)`), "names a place where secrets live"},
	{RuleBang, regexp.MustCompile("(!`|^\\s*```!)"), "native command injection syntax"},
	{RuleParentEscape, regexp.MustCompile(`(^|[\s"'` + "`" + `(=<])\.\./`), "reads outside the skill directory"},
}

const maxScanFile = 1 << 20

// Scan checks every skill of every pack, including disabled ones.
func Scan(lib *library.Library) ([]Hit, error) {
	var hits []Hit
	m := lib.Manifest
	for _, name := range m.PackNames() {
		p := m.Packs[name]
		recs, _ := lib.ScanPack(p, true)
		for _, r := range recs {
			h, err := scanSkill(lib, p, r)
			if err != nil {
				return nil, err
			}
			hits = append(hits, h...)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Skill != b.Skill {
			return a.Skill < b.Skill
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return hits, nil
}

func scanSkill(lib *library.Library, p *manifest.Pack, r *library.Record) ([]Hit, error) {
	sev := High
	if p.Trust == manifest.Trusted {
		sev = Warn
	}
	add := func(hits *[]Hit, rule, file string, line int, detail string) {
		*hits = append(*hits, Hit{Severity: sev, Rule: rule, Pack: p.Name, Skill: r.ID, File: file, Line: line, Detail: detail})
	}
	var hits []Hit
	files, _, err := lib.Inventory(r)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Symlink {
			continue
		}
		if f.Script {
			add(&hits, RuleScript, f.Path, 0, "the skill bundles a script")
		}
		if f.Size > maxScanFile {
			continue
		}
		data, err := lib.ReadFileBounded(path.Join(r.Dir, f.Path))
		if err != nil {
			if isSkip(err) {
				continue
			}
			return nil, err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue // binary
		}
		if f.Path == library.SkillFile {
			if fm, _, _, err := library.ParseSkill(data); err == nil && broadTools(fm.Fields["allowed-tools"]) {
				add(&hits, RuleBroadTools, f.Path, 0, "allowed-tools grants every tool or all of Bash")
			}
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, rule := range lineRules {
				if rule.re.MatchString(line) {
					add(&hits, rule.name, f.Path, i+1, rule.note)
				}
			}
		}
	}
	return hits, nil
}

// isSkip reports a read error that only means "do not scan this file".
func isSkip(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "larger than")
}

// broadTools reports whether an allowed-tools value grants every tool or all of
// Bash. The value is a string or a list of strings.
func broadTools(v any) bool {
	var items []string
	switch t := v.(type) {
	case string:
		items = strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' })
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				items = append(items, s)
			}
		}
	}
	for _, it := range items {
		switch strings.TrimSpace(it) {
		case "*", "Bash", "Bash(*)", "Bash(*:*)":
			return true
		}
	}
	return false
}

// String formats a hit for one line of output.
func (h Hit) String() string {
	loc := h.File
	if h.Line > 0 {
		loc = fmt.Sprintf("%s:%d", h.File, h.Line)
	}
	return fmt.Sprintf("%s: %s (%s)", loc, h.Rule, h.Detail)
}
