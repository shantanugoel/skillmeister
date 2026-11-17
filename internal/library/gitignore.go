package library

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
)

// GitignoreFile is the ignore file at the library root.
const GitignoreFile = ".gitignore"

// GitignoreLines are the lines that a library .gitignore must hold. The first
// four keep skillmeister's own state out of git. The others keep the output of
// a skill setup out of git: a virtual environment, bytecode, and dotenv files
// that can hold secrets. The example forms of a dotenv file stay tracked,
// because a pack can ship them and its tree hash includes them.
var GitignoreLines = []string{
	".import/",
	".removed/",
	".skillmeister.lock",
	"node_modules/",
	".venv/",
	"__pycache__/",
	"*.pyc",
	".env",
	".env.*",
	"!.env.example",
	"!.env.sample",
	"!.env.template",
}

// envGroup must stay in this order: a negation only works after the pattern
// that it undoes.
var envGroup = []string{".env.*", "!.env.example", "!.env.sample", "!.env.template"}

// MissingGitignore returns the lines that the library .gitignore must get, in
// the order to append them. A line counts as present only on an exact match.
// When `.env.*` is missing, the negations come again after it.
func MissingGitignore(l *Library) ([]string, error) {
	data, err := l.Root.ReadFile(GitignoreFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, line := range GitignoreLines {
		if !have[line] || (!have[envGroup[0]] && slices.Contains(envGroup, line)) {
			missing = append(missing, line)
		}
	}
	return missing, nil
}
