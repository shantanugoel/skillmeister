package library

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
)

// MaxFrontmatter is the largest accepted frontmatter block, in bytes.
const MaxFrontmatter = 64 << 10

// Frontmatter is the parsed header of a SKILL.md file.
type Frontmatter struct {
	Name        string
	Description string
	License     string
	// Fields holds every top-level key.
	Fields map[string]any
	// Raw is the YAML text between the fences.
	Raw string
}

var bom = []byte("\xef\xbb\xbf")

// ParseSkill splits SKILL.md bytes into frontmatter and body. When the file has
// no frontmatter, has is false and the whole file is the body.
func ParseSkill(data []byte) (fm Frontmatter, has bool, body []byte, err error) {
	data = bytes.TrimPrefix(data, bom)
	first, rest, ok := cutLine(data)
	if !ok || !isFence(first) {
		return Frontmatter{}, false, data, nil
	}
	// Find the closing fence. The block may not exceed MaxFrontmatter bytes.
	tooBig := fmt.Errorf("frontmatter is larger than %d KB", MaxFrontmatter>>10)
	end, after := -1, 0
	for pos := 0; ; {
		line, next, more := cutLine(rest[pos:])
		if isFence(line) {
			end, after = pos, len(rest)-len(next)
			break
		}
		if !more {
			break
		}
		pos = len(rest) - len(next)
		if pos > MaxFrontmatter {
			return fm, true, nil, tooBig
		}
	}
	if end < 0 {
		return fm, true, nil, errors.New("frontmatter fence is not closed")
	}
	block := rest[:end]
	if !utf8.Valid(block) {
		return fm, true, nil, errors.New("frontmatter is not valid UTF-8")
	}
	body = rest[after:]
	fm, err = parseBlock(block)
	return fm, true, body, err
}

// cutLine returns the first line without its terminator, the remainder, and
// whether a terminator was found.
func cutLine(b []byte) (line, rest []byte, ok bool) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return b, nil, false
	}
	return b[:i], b[i+1:], true
}

// isFence reports whether the line is `---`, allowing CRLF and trailing blanks.
func isFence(line []byte) bool {
	line = bytes.TrimRight(line, " \t\r")
	return string(line) == "---"
}

func parseBlock(block []byte) (Frontmatter, error) {
	fm := Frontmatter{Raw: string(block), Fields: map[string]any{}}
	if len(bytes.TrimSpace(block)) == 0 {
		return fm, nil
	}
	file, err := parser.ParseBytes(block, 0)
	if err != nil {
		return fm, fmt.Errorf("frontmatter: %s", firstLine(yaml.FormatError(err, false, false)))
	}
	if len(file.Docs) > 1 {
		return fm, errors.New("frontmatter has multiple YAML documents")
	}
	if len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return fm, nil
	}
	if err := yaml.NodeToValue(file.Docs[0].Body, &fm.Fields); err != nil {
		return fm, fmt.Errorf("frontmatter: %s", firstLine(yaml.FormatError(err, false, false)))
	}
	if fm.Fields == nil {
		fm.Fields = map[string]any{}
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"name", &fm.Name}, {"description", &fm.Description}, {"license", &fm.License}} {
		v, ok := fm.Fields[f.key]
		if !ok || v == nil {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			return fm, fmt.Errorf("frontmatter: %s must be a string", f.key)
		}
		*f.dst = collapse(s)
	}
	return fm, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// collapse folds runs of whitespace, including newlines, to single spaces.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
