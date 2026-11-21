package pack

import "testing"

func TestParseNPXSource(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct{ in, repo, ref, with string }{
		{"o/r", "https://github.com/o/r", "", "o/r#" + sha},
		{"o/r#v1", "https://github.com/o/r", "v1", "o/r#" + sha},
		{"o/r#v1@pdf", "https://github.com/o/r", "v1", "o/r#" + sha + "@pdf"},
		{"o/r#pkg%401.0", "https://github.com/o/r", "pkg@1.0", "o/r#" + sha},
		{"o/r@1.2.0", "https://github.com/o/r", "1.2.0", "o/r#" + sha},
		{"o/r@pdf", "https://github.com/o/r", "", "o/r@pdf#" + sha},
		{"o/r/skills/pdf", "https://github.com/o/r", "", "o/r/skills/pdf#" + sha},
		{"https://github.com/o/r", "https://github.com/o/r", "", "https://github.com/o/r#" + sha},
		{"https://github.com/o/r.git", "https://github.com/o/r", "", "https://github.com/o/r.git#" + sha},
		{"https://github.com/o/r/tree/main/skills/pdf", "https://github.com/o/r", "main", "https://github.com/o/r/tree/" + sha + "/skills/pdf"},
		{"git@github.com:o/r.git", "git@github.com:o/r.git", "", "git@github.com:o/r.git#" + sha},
		{"notion", "", "", "notion#" + sha},
		{"https://gitlab.com/g/r", "", "", "https://gitlab.com/g/r#" + sha},
		{"github:o/r#v1", "https://github.com/o/r", "v1", "github:o/r#" + sha},
		{"github.com/o/r", "https://github.com/o/r", "", "github.com/o/r#" + sha},
		{"http://www.github.com/o/r/tree/v1/x", "https://github.com/o/r", "v1", "http://www.github.com/o/r/tree/" + sha + "/x"},
		{"vercel-labs/vercel-skills", "https://github.com/vercel-labs/agent-skills", "", "vercel-labs/agent-skills#" + sha},
		{"o/r@ABCDEF0123456789ABCDEF0123456789ABCDEF01", "https://github.com/o/r", "ABCDEF0123456789ABCDEF0123456789ABCDEF01", "o/r#" + sha},
		{"o/r#ABCDEF0123456789ABCDEF0123456789ABCDEF01", "https://github.com/o/r", "ABCDEF0123456789ABCDEF0123456789ABCDEF01", "o/r#" + sha},
		{"o/r@1.2.0#main", "https://github.com/o/r", "main", "o/r@1.2.0#" + sha},
	} {
		s := parseNPXSource(c.in)
		if s.repo != c.repo || s.ref != c.ref || s.with(sha) != c.with {
			t.Errorf("%s: repo %q ref %q with %q", c.in, s.repo, s.ref, s.with(sha))
		}
	}
	if !parseNPXSource("Vercel-Labs/agent-skills").snapshot() || !parseNPXSource("zapier/connectors").snapshot() || parseNPXSource("o/r").snapshot() {
		t.Error("snapshot owners")
	}
	t.Setenv("GH_HOST", "ghe.example.com")
	if s := parseNPXSource("o/r"); s.repo != "" {
		t.Errorf("GH_HOST: repo %q", s.repo)
	}
	t.Setenv("GH_HOST", "")
	if got := parseNPXSource("o/r").with("pkg@1.0#x y"); got != "o/r#pkg%401.0%23x%20y" {
		t.Errorf("encode: %s", got)
	}
}

func TestNormRef(t *testing.T) {
	if normRef("ABCDEF0123456789ABCDEF0123456789ABCDEF01") != "abcdef0123456789abcdef0123456789abcdef01" || normRef("Main") != "Main" {
		t.Error("normRef")
	}
}

func TestRequiredNode(t *testing.T) {
	if v := requiredNode(); len(v) != 3 || v[0] < 18 {
		t.Errorf("requiredNode = %v", v)
	}
	if v := parseVersion("v22.20.1"); v[0] != 22 || v[1] != 20 || v[2] != 1 {
		t.Errorf("parseVersion = %v", v)
	}
}

func TestCleanCLIOutput(t *testing.T) {
	in := "│\n◒  Cloning…\x1b[1G\x1b[J◐  Cloning….│\n■  \x1b[31mFailed to clone\x1b[39m\n│\n│  \x1b[2m[SKILLS_GIT_AUTH_FAILED] Auth failed.\x1b[22m\n"
	want := "◐  Cloning….│\n■  Failed to clone\n│  [SKILLS_GIT_AUTH_FAILED] Auth failed."
	if got := cleanCLIOutput(in); got != want {
		t.Errorf("got %q", got)
	}
}
