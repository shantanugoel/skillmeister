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
	// RuleIncomplete marks a skill that the scan could not read in full.
	RuleIncomplete = "incomplete-scan"
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
	{RuleCurlPipe, regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo(\s+-\S+)*\s+)?(env\s+)?(/\S*/)?(ba|z|da|k)?sh\b`), "downloads and runs code in one step"},
	{RuleCurlPipe, regexp.MustCompile(`(?i)\b(ba|z|da|k)?sh\s+(-c\s+["']?\$\(|<\()\s*(curl|wget)\b`), "downloads and runs code in one step"},
	{RuleSudo, regexp.MustCompile(`(^|[^A-Za-z0-9_-])sudo\s`), "asks for root"},
	{RuleEval, regexp.MustCompile(`(^|[^A-Za-z0-9_.-])eval[\s(]+["'$` + "`" + `A-Za-z(]`), "runs a constructed string"},
	{RuleBase64, regexp.MustCompile(`base64\s+(-\w*[dD]\w*|--decode)|[A-Za-z0-9+/]{160,}={0,2}`), "decodes or holds an opaque blob"},
	{RuleSecretPath, regexp.MustCompile(`(?i)(\.ssh/|\.aws/|\.gnupg/|\.kube/config|\.docker/config|id_rsa|id_ed25519|/etc/shadow|\.netrc|\.npmrc|\.pypirc|credentials\.json|(^|[\s'"/])\.env\b|keychain)`), "names a place where secrets live"},
	{RuleBang, regexp.MustCompile("(!`|^\\s*```!)"), "native command injection syntax"},
	{RuleParentEscape, parentRE, "reads outside the pack directory"},
}

// parentRE finds a run of ../ segments at the start of a path. Group 1 is the
// character before the run, group 2 is the run, and group 3 is a final bare ..
// (as in ../..).
var parentRE = regexp.MustCompile(`(^|[\s"'` + "`" + `(=<}/])((?:\.\./)+)(\.\.(?:[^\w./]|$))?`)

// skillDirVars name the skill directory in a body.
var skillDirVars = []string{"${CLAUDE_SKILL_DIR}", "$CLAUDE_SKILL_DIR", "<SKILL_DIR>"}

// leavesPack reports whether a line has a ../ path that leaves the pack
// directory. A skill may use the files of the other skills in its pack, so a
// path that goes up to the pack directory and no further is not a hit. file is
// the path of the file relative to the skill directory.
//
// The start of the path sets how far up it can go:
//   - no prefix: the directory of the file;
//   - a skill-directory placeholder such as ${CLAUDE_SKILL_DIR}/: the skill
//     directory;
//   - a relative name such as references/: the directory of the file, or lower;
//   - any other prefix (/x/, ~/, $HOME/, <SKILLS>/): unknown, so no level up is
//     safe.
func leavesPack(line, file string) bool {
	// Levels from the directory of the file up to the pack directory.
	fromFile := 1 + strings.Count(file, "/")
	for _, m := range parentRE.FindAllStringSubmatchIndex(line, -1) {
		up := (m[5] - m[4]) / len("../")
		if m[6] >= 0 {
			up++
		}
		limit := fromFile
		if m[3] > m[2] && line[m[2]:m[3]] == "/" {
			limit = prefixLimit(line[:m[2]], fromFile)
		}
		if up > limit {
			return true
		}
	}
	return false
}

// prefixLimit returns how many levels a path can go up after the text before
// its slash.
func prefixLimit(before string, fromFile int) int {
	for _, v := range skillDirVars {
		if strings.HasSuffix(before, v) {
			return 1
		}
	}
	tok := before[strings.LastIndexAny(before, " \t\"'`(=<")+1:]
	if tok == "" || strings.ContainsAny(tok[:1], "/~$<{") || strings.ContainsAny(tok, ":$<>{}") {
		return 0
	}
	return fromFile
}

const maxScanFile = 1 << 20

// Scan checks every skill of every pack, including disabled ones.
func Scan(lib *library.Library) ([]Hit, error) {
	var hits []Hit
	m := lib.Manifest
	for _, name := range m.PackNames() {
		p := m.Packs[name]
		recs, warns := lib.ScanPack(p, true)
		// A skill that cannot be parsed or read is a skill that the scan cannot
		// vouch for. Report it instead of a silent zero.
		for _, w := range warns {
			hits = append(hits, Hit{Severity: sevFor(p), Rule: RuleIncomplete, Pack: p.Name, Skill: p.Name, File: "-", Detail: w})
		}
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

func sevFor(p *manifest.Pack) Severity {
	if p.Trust == manifest.Trusted {
		return Warn
	}
	return High
}

func scanSkill(lib *library.Library, p *manifest.Pack, r *library.Record) ([]Hit, error) {
	sev := sevFor(p)
	add := func(hits *[]Hit, rule, file string, line int, detail string) {
		*hits = append(*hits, Hit{Severity: sev, Rule: rule, Pack: p.Name, Skill: r.ID, File: file, Line: line, Detail: detail})
	}
	var hits []Hit
	files, truncated, err := lib.Inventory(r)
	if err != nil {
		return nil, err
	}
	if truncated {
		add(&hits, RuleIncomplete, "-", 0, "the skill has too many files; some were not scanned")
	}
	for _, f := range files {
		if f.Symlink {
			continue
		}
		if f.Script {
			add(&hits, RuleScript, f.Path, 0, "the skill bundles a script")
		}
		if f.Size > maxScanFile {
			add(&hits, RuleIncomplete, f.Path, 0, "the file is larger than 1 MB and was not scanned")
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
				if rule.re.MatchString(line) && (rule.name != RuleParentEscape || leavesPack(line, f.Path)) {
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
		it = strings.ToLower(strings.TrimSpace(it))
		switch it {
		case "*", "bash", "bash(*)", "bash(*:*)":
			return true
		}
		// A grant of an interpreter or a shell is a grant of everything.
		for _, interp := range []string{"bash", "sh", "zsh", "python", "python3", "node", "perl", "ruby", "env", "sudo", "eval"} {
			if strings.HasPrefix(it, "bash("+interp+":") || strings.HasPrefix(it, "bash("+interp+" ") {
				return true
			}
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
