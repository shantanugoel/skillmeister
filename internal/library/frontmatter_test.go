package library

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseSkill(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		has      bool
		skillNm  string
		desc     string
		body     string
		errMatch string
	}{
		{"plain", "---\nname: a\ndescription: Does a thing.\n---\nbody\n", true, "a", "Does a thing.", "body\n", ""},
		{"crlf", "---\r\nname: a\r\ndescription: Does a thing.\r\n---\r\nbody\r\n", true, "a", "Does a thing.", "body\r\n", ""},
		{"bom", "\xef\xbb\xbf---\nname: a\n---\nbody", true, "a", "", "body", ""},
		{"no frontmatter", "# Title\n\nbody\n", false, "", "", "# Title\n\nbody\n", ""},
		{"empty file", "", false, "", "", "", ""},
		{"empty block", "---\n---\nbody", true, "", "", "body", ""},
		{"fence at eof", "---\nname: a\n---", true, "a", "", "", ""},
		{"trailing blanks on fence", "---  \nname: a\n---\t\nbody", true, "a", "", "body", ""},
		{"folded description", "---\ndescription: >\n  one\n  two\n---\nb", true, "", "one two", "b", ""},
		{"dashes in body", "---\nname: a\n---\nx\n---\ny\n", true, "a", "", "x\n---\ny\n", ""},
		{"unterminated", "---\nname: a\n", true, "", "", "", "not closed"},
		{"invalid utf8", "---\nname: \xff\xfe\n---\n", true, "", "", "", "UTF-8"},
		{"duplicate key", "---\nname: a\nname: b\n---\n", true, "", "", "", "already defined"},
		{"multiple documents", "---\nname: a\n...\nname: b\n---\n", true, "", "", "", "multiple YAML documents"},
		{"non-string name", "---\nname: [a]\n---\n", true, "", "", "", "must be a string"},
		{"list at top", "---\n- a\n- b\n---\n", true, "", "", "", "mapping is expected"},
		{"bad yaml", "---\nname: [a\n---\n", true, "", "", "", "frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, has, body, err := ParseSkill([]byte(tt.in))
			if tt.errMatch != "" {
				if err == nil {
					t.Fatalf("want error containing %q", tt.errMatch)
				}
				if !strings.Contains(err.Error(), tt.errMatch) {
					t.Fatalf("error %q lacks %q", err, tt.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if has != tt.has || fm.Name != tt.skillNm || fm.Description != tt.desc || string(body) != tt.body {
				t.Fatalf("got has=%v name=%q desc=%q body=%q", has, fm.Name, fm.Description, body)
			}
		})
	}
}

func TestParseSkillSizeBoundary(t *testing.T) {
	// A frontmatter block of exactly MaxFrontmatter bytes passes; one more byte fails.
	mk := func(size int) []byte {
		head := "description: "
		pad := size - len(head) - 1
		block := head + strings.Repeat("a", pad) + "\n"
		return []byte("---\n" + block + "---\nbody")
	}
	if _, _, _, err := ParseSkill(mk(MaxFrontmatter)); err != nil {
		t.Fatalf("64 KB block rejected: %v", err)
	}
	if _, _, _, err := ParseSkill(mk(MaxFrontmatter + 1)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("64 KB + 1 accepted or wrong error: %v", err)
	}
	// An unterminated block bigger than the limit reports the size error.
	big := []byte("---\n" + strings.Repeat("a: b\n", MaxFrontmatter))
	if _, _, _, err := ParseSkill(big); err == nil {
		t.Fatal("oversized unterminated block accepted")
	}
}

func FuzzParseSkill(f *testing.F) {
	seeds := []string{
		"---\nname: a\ndescription: d\n---\nbody",
		"\xef\xbb\xbf---\r\nname: a\r\n---\r\nbody",
		"---\n---\n",
		"---",
		"---\n" + strings.Repeat("k: v\n", 100) + "---\n",
		"no frontmatter",
		"---\nname: \xff\n---\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fm, has, body, err := ParseSkill(in)
		if err != nil {
			return
		}
		if !has && string(body) != string(in) && !strings.HasPrefix(string(in), "\xef\xbb\xbf") {
			t.Fatalf("no frontmatter but body differs from input")
		}
		if has {
			if len(fm.Raw) > MaxFrontmatter {
				t.Fatalf("accepted a block of %d bytes", len(fm.Raw))
			}
			if !utf8.ValidString(fm.Raw) {
				t.Fatal("accepted invalid UTF-8")
			}
		}
		if len(body) > len(in) {
			t.Fatal("body longer than input")
		}
	})
}

func TestDetectWarnings(t *testing.T) {
	fm, _, body, err := ParseSkill([]byte("---\nname: x\nallowed-tools: Bash(git:*)\ncontext: fork\nhooks:\n  PreToolUse: []\n---\nRun !`date` and ${CLAUDE_SKILL_DIR}/run.sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	w := DetectWarnings(fm, body)
	if len(w) != 5 {
		t.Fatalf("want 5 warnings, got %v", w)
	}
	fm, _, body, _ = ParseSkill([]byte("---\nname: x\ncontext: inline\n---\n```!\nls\n```\n"))
	if w := DetectWarnings(fm, body); len(w) != 1 || w[0] != warnBang {
		t.Fatalf("fenced bang: %v", w)
	}
	fm, _, body, _ = ParseSkill([]byte("---\nname: x\n---\nif [ ! -f x ]; then echo hi; fi\n"))
	if w := DetectWarnings(fm, body); len(w) != 0 || w == nil {
		t.Fatalf("clean skill must give an empty, non-nil slice: %#v", w)
	}
}
