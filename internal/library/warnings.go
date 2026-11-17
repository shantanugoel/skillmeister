package library

import (
	"bytes"
	"fmt"
)

// Warning texts for native-only features. A routed skill is plain text, so
// these features do nothing.
const (
	warnAllowedTools = "allowed-tools is not enforced in a routed skill"
	warnFork         = "context: fork does not work in a routed skill"
	warnHooks        = "hooks do not run in a routed skill"
	warnBang         = "! command injection does not run in a routed skill"
	warnSkillDir     = "${CLAUDE_SKILL_DIR} is not substituted in a routed skill"
)

// DetectWarnings returns the native-only features that a skill uses. The result
// is never nil and its order is stable.
func DetectWarnings(fm Frontmatter, body []byte) []string {
	w := []string{}
	if _, ok := fm.Fields["allowed-tools"]; ok {
		w = append(w, warnAllowedTools)
	}
	if v, ok := fm.Fields["context"]; ok && fmt.Sprint(v) == "fork" {
		w = append(w, warnFork)
	}
	if _, ok := fm.Fields["hooks"]; ok {
		w = append(w, warnHooks)
	}
	if hasBangInjection(body) {
		w = append(w, warnBang)
	}
	if bytes.Contains(body, []byte("${CLAUDE_SKILL_DIR}")) {
		w = append(w, warnSkillDir)
	}
	return w
}

// hasBangInjection finds the two native forms of command injection: an inline
// !`cmd` span and a fenced block that opens with ```!.
func hasBangInjection(body []byte) bool {
	if bytes.Contains(body, []byte("!`")) {
		return true
	}
	for line := range bytes.Lines(body) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("```!")) {
			return true
		}
	}
	return false
}
