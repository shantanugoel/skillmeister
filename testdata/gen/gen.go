// Package gen materializes a deterministic synthetic skill library for the
// performance and golden tests, and for the 100-skill definition-of-done
// library. A small set of curated skills carries known queries. The filler
// skills use a vocabulary that never overlaps the query words, so the expected
// results stay stable when the filler count changes.
package gen

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Seed makes the output byte-stable.
const Seed = 20261117

// Query is one fixture query. Want must be inside the top three results.
type Query struct {
	Query string
	Want  string
}

// Fixture describes a materialized library.
type Fixture struct {
	Root    string
	Skills  int
	Queries []Query
}

type curated struct {
	pack, name, desc, body string
}

var curatedSkills = []curated{
	{"own", "pdf-tools", "Extract text and tables from PDF files and convert them to Markdown.", "Run pdftotext with the layout flag. Rebuild tables from column gaps."},
	{"own", "commit-writer", "Write clear git commit messages from a staged diff in conventional commit style.", "Run git diff --staged. Pick a type. Keep the subject short."},
	{"own", "meeting-notes", "Turn a raw meeting transcript into notes with decisions and action items.", "List decisions first. Then list owners and due dates."},
	{"own", "release-notes", "Draft release notes for a new version from merged pull requests.", "Group changes by area. Follow semver rules when you pick the version number."},
	{"own", "sql-review", "Review a SQL query for correctness, index use, and unsafe statements.", "Check joins, filters, and missing indexes. Flag any DELETE without WHERE."},
	{"own", "code-review", "Review a code change for bugs, style problems, and missing tests.", "Read the diff twice. Report only real problems with a file and line."},
	{"anthropic", "pdf-forms", "Fill in and flatten PDF forms with field mapping.", "List the form fields first. Map each field to an input value."},
	{"anthropic", "docx-editor", "Create and edit Word documents with styles, headings, and tables.", "Use named styles. Do not format text by hand."},
	{"anthropic", "xlsx-builder", "Build spreadsheets with formulas, charts, and clean formatting.", "Keep raw data on one sheet and summaries on another."},
	{"anthropic", "slide-deck", "Design a slide deck from an outline with consistent layout.", "One idea for each slide. Use large type."},
	{"anthropic", "web-artifacts", "Build self-contained HTML pages and small interactive tools.", "Inline all styles and scripts. Avoid external fonts."},
	{"anthropic", "skill-creator", "Create and improve agent skills, with evals and description tuning.", "Write the description first. Test with real prompts."},
	{"anthropic", "mcp-builder", "Build MCP servers that expose tools to a language model.", "Define one tool for each task. Validate all inputs."},
	{"anthropic", "brand-guidelines", "Apply brand colors and typography to generated documents.", "Use the approved palette only."},
	{"community", "test-driven-development", "Write the failing test first, then the code, then refactor.", "Red, green, refactor. Run the tests after each step."},
	{"community", "systematic-debugging", "Find the root cause of a bug with evidence before you change code.", "Reproduce first. Form one hypothesis. Test it."},
	{"community", "brainstorming", "Explore intent and requirements with the user before you design.", "Ask one question at a time. Offer options."},
	{"community", "writing-plans", "Write a step-by-step implementation plan from a spec.", "Each step must be small and testable."},
	{"community", "git-worktrees", "Create isolated git worktrees for parallel feature work.", "Run git worktree add with a new branch name."},
	{"community", "docker-compose-debug", "Debug container startup problems in a compose stack.", "Read the logs of the failing service. Check the health checks."},
	{"community", "terraform-plan-review", "Review a terraform plan for destructive changes and drift.", "Look for resources marked for replacement."},
	{"community", "api-docs", "Write API reference documentation from source code and examples.", "Document every parameter and error code."},
	{"community", "accessibility-audit", "Audit a web page for accessibility problems against WCAG.", "Check contrast, labels, and keyboard focus order."},
	{"community", "regex-helper", "Build and explain regular expressions with test strings.", "Start with the simplest pattern. Add cases one at a time."},
}

// queries are the 20 fixture queries. Each covers one ranking tier or a
// multi-token case.
var queries = []Query{
	{"pdf", "own/pdf-tools"},                            // prefix, own wins the tie
	{"pdf forms", "anthropic/pdf-forms"},                // prefix, two tokens
	{"pdf-tools", "own/pdf-tools"},                      // exact name
	{"own/commit-writer", "own/commit-writer"},          // exact ID
	{"commit", "own/commit-writer"},                     // prefix
	{"writer", "own/commit-writer"},                     // name token
	{"transcript", "own/meeting-notes"},                 // description only
	{"conventional commit", "own/commit-writer"},        // description tokens
	{"semver", "own/release-notes"},                     // body only
	{"sql", "own/sql-review"},                           // prefix
	{"review", "own/code-review"},                       // name token, own tie-break
	{"terraform", "community/terraform-plan-review"},    // prefix
	{"worktree", "community/git-worktrees"},             // name token
	{"debugging", "community/systematic-debugging"},     // name token
	{"wcag", "community/accessibility-audit"},           // description only
	{"mcp", "anthropic/mcp-builder"},                    // prefix
	{"slide deck", "anthropic/slide-deck"},              // prefix, two tokens
	{"regex", "community/regex-helper"},                 // prefix
	{"compose", "community/docker-compose-debug"},       // name token
	{"spreadsheets formulas", "anthropic/xlsx-builder"}, // description tokens
}

// Filler vocabulary. None of these words may appear in a curated name, a
// curated description, or a query.
var (
	adjs = []string{"quarterly", "legacy", "async", "weekly", "regional", "offline", "bulk", "nightly",
		"manual", "hybrid", "remote", "seasonal", "archival", "staged", "tiered", "dormant"}
	nouns = []string{"invoice", "ledger", "orchard", "telemetry", "inventory", "payroll", "garden",
		"warehouse", "roster", "itinerary", "catalog", "harvest", "voucher", "lexicon", "turbine",
		"beacon", "quarry", "mosaic", "pantry", "kiln"}
	verbs = []string{"reconcile", "summarize", "categorize", "forecast", "validate", "archive",
		"schedule", "audit", "translate", "tabulate", "estimate", "normalize"}
	audiences = []string{"small teams", "field staff", "new hires", "finance owners", "shift leads"}
	sentences = []string{
		"Start by reading the input in full before you change anything.",
		"Keep each step small and check the result before the next step.",
		"If the data looks wrong, stop and ask the user what to do.",
		"Write the result to a new file. Do not overwrite the source.",
		"List the assumptions that you made at the end of the answer.",
		"Use plain words and short sentences in every report.",
		"When two options fit, show both and say which one you prefer.",
		"Save intermediate notes so that you can resume after a break.",
		"Name every output file with the date and a short label.",
		"Run the check again after each fix and report the final state.",
	}
)

// Materialize writes a library of n skills under dir. It needs n of at least
// the number of curated skills.
func Materialize(dir string, n int) (*Fixture, error) {
	if n < len(curatedSkills) {
		return nil, fmt.Errorf("gen: n must be at least %d", len(curatedSkills))
	}
	rng := rand.New(rand.NewPCG(Seed, 1))
	packs := []packSpec{{"own", "local", "trusted"}, {"anthropic", "npx", "pinned"}, {"community", "git", "pinned"}}

	type skill struct{ pack, name, desc, body string }
	var skills []skill
	used := map[string]bool{}
	for _, c := range curatedSkills {
		skills = append(skills, skill{c.pack, c.name, c.desc, body(rng, c.name, c.body)})
		used[c.pack+"/"+c.name] = true
	}
	for i := 0; len(skills) < n; i++ {
		p := pickPack(i)
		name := adjs[rng.IntN(len(adjs))] + "-" + nouns[rng.IntN(len(nouns))]
		if rng.IntN(3) == 0 {
			name = verbs[rng.IntN(len(verbs))] + "-" + nouns[rng.IntN(len(nouns))]
		}
		if used[p+"/"+name] {
			name = fmt.Sprintf("%s-%d", name, i)
		}
		if used[p+"/"+name] {
			continue
		}
		used[p+"/"+name] = true
		desc := fmt.Sprintf("%s %s records and %s %s for %s.",
			cap1(verbs[rng.IntN(len(verbs))]), nouns[rng.IntN(len(nouns))],
			verbs[rng.IntN(len(verbs))], nouns[rng.IntN(len(nouns))], audiences[rng.IntN(len(audiences))])
		skills = append(skills, skill{p, name, desc, body(rng, name, "")})
	}

	var manifest strings.Builder
	manifest.WriteString("version: 1\npacks:\n")
	for _, p := range packs {
		fmt.Fprintf(&manifest, "  %s:\n    fetch: %s\n", p.name, p.fetch)
		switch p.fetch {
		case "local":
			manifest.WriteString("    path: skills\n")
		case "npx":
			manifest.WriteString("    source: example/skills\n    ref: 8f3c2a1\n")
		case "git":
			manifest.WriteString("    url: https://example.com/community.git\n    ref: v1.0.0\n")
		}
		fmt.Fprintf(&manifest, "    trust: %s\n    enabled: true\n", p.trust)
	}
	manifest.WriteString("profiles:\n  work: [own, anthropic]\ndefaults:\n  search_limit: 8\n")
	if err := write(filepath.Join(dir, "skillmeister.yaml"), manifest.String()); err != nil {
		return nil, err
	}
	for i, s := range skills {
		base := filepath.Join(dir, "packs", s.pack)
		if s.pack == "own" {
			base = filepath.Join(dir, "skills")
		}
		extra := ""
		if i%23 == 5 { // a few skills use a native-only field
			extra = "allowed-tools: Bash(git:*)\n"
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n%s---\n\n%s", s.name, s.desc, extra, s.body)
		if err := write(filepath.Join(base, s.name, "SKILL.md"), content); err != nil {
			return nil, err
		}
	}
	return &Fixture{Root: dir, Skills: len(skills), Queries: append([]Query(nil), queries...)}, nil
}

type packSpec struct{ name, fetch, trust string }

// pickPack spreads filler over the packs: 2 of every 10 go to own, 4 to
// anthropic, and 4 to community.
func pickPack(i int) string {
	switch slot := i % 10; {
	case slot < 2:
		return "own"
	case slot < 6:
		return "anthropic"
	default:
		return "community"
	}
}

func body(rng *rand.Rand, name, lead string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.ReplaceAll(name, "-", " "))
	if lead != "" {
		b.WriteString(lead + "\n\n")
	}
	b.WriteString("## When to use\n\n")
	for range 2 + rng.IntN(3) {
		b.WriteString(sentences[rng.IntN(len(sentences))] + " ")
	}
	b.WriteString("\n\n## Steps\n\n")
	for i := range 3 + rng.IntN(5) {
		fmt.Fprintf(&b, "%d. %s\n", i+1, sentences[rng.IntN(len(sentences))])
	}
	b.WriteString("\n## Notes\n\n")
	// Pad the body to a realistic 1 to 4 KB.
	target := 1024 + rng.IntN(3072)
	for b.Len() < target {
		b.WriteString(sentences[rng.IntN(len(sentences))] + " ")
		if rng.IntN(4) == 0 {
			b.WriteString("\n\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func cap1(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// IDs returns the sorted skill IDs of the curated set. Tests use it to check
// that the curated skills exist.
func IDs() []string {
	ids := make([]string, len(curatedSkills))
	for i, c := range curatedSkills {
		ids[i] = c.pack + "/" + c.name
	}
	sort.Strings(ids)
	return ids
}
