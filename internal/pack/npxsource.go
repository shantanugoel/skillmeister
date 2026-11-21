package pack

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

// npxSource is a source of the `skills` CLI, split so that skillmeister can
// replace the ref that it carries. A source can carry a ref in three ways: a
// fragment (o/r#v1), a GitHub tree URL (https://github.com/o/r/tree/v1/sub),
// or a version pin on a shorthand (o/r@1.2.0).
type npxSource struct {
	// repo is the GitHub URL that git ls-remote can query, or empty.
	repo string
	// owner and name identify the GitHub repo, in lower case.
	owner, name string
	// ref is the ref that the source carries, or empty.
	ref string
	// with builds the source text with a different ref.
	with func(ref string) string
}

// shorthandRE matches owner/repo, owner/repo/subpath, and owner/repo@x, the
// forms that the `skills` CLI reads as a GitHub repo.
var shorthandRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(?:/(.+)|@(.+))?$`)

// versionPinRE matches the @ suffix that the `skills` CLI reads as a ref, not
// as a skill filter: a version such as 1.2.0 or v1.2.0-rc.1, or a full hash.
var versionPinRE = regexp.MustCompile(`^(v?\d+(\.\d+)+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-fA-F]{40})$`)

// githubURLRE matches a github.com repo URL with an optional tree path. The
// CLI also accepts http://, www., and no scheme.
var githubURLRE = regexp.MustCompile(`^((?:https?://)?(?:www\.)?github\.com)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(\.git)?(?:/tree/([^/]+)(/.*)?)?/?$`)

// sshURLRE matches git@github.com:owner/repo(.git).
var sshURLRE = regexp.MustCompile(`^git@github\.com:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(\.git)?$`)

// sourceAliases are the sources that the `skills` CLI 1.9.0 renames.
var sourceAliases = map[string]string{
	"coinbase/agentWallet":      "coinbase/agentic-wallet-skills",
	"vercel-labs/vercel-skills": "vercel-labs/agent-skills",
}

func parseNPXSource(src string) npxSource {
	if inner, ok := strings.CutPrefix(src, "github:"); ok {
		s := parseNPXSource(inner)
		with := s.with
		s.with = func(ref string) string { return "github:" + with(ref) }
		return s
	}
	s := npxSource{}
	base, frag, hasFrag := strings.Cut(src, "#")
	if alias, ok := sourceAliases[base]; ok {
		base = alias
	}
	var filter string
	if hasFrag {
		r, f, _ := strings.Cut(frag, "@")
		s.ref, filter = decodeFrag(r), f
	}
	// withFragment appends a ref and keeps the skill filter of the fragment.
	withFragment := func(b string) func(string) string {
		return func(ref string) string {
			out := b + "#" + encodeFrag(ref)
			if filter != "" {
				out += "@" + filter
			}
			return out
		}
	}
	s.with = withFragment(base)
	switch {
	case githubURLRE.MatchString(base):
		m := githubURLRE.FindStringSubmatch(base)
		s.repo, s.owner, s.name = "https://github.com/"+m[2]+"/"+m[3], m[2], m[3]
		if m[5] != "" {
			// The CLI prefers the tree ref to a fragment ref, and it does not
			// decode the tree ref.
			s.ref = m[5]
			prefix, sub := m[1]+"/"+m[2]+"/"+m[3], m[6]
			s.with = func(ref string) string {
				out := prefix + "/tree/" + ref + sub
				if filter != "" {
					out += "#@" + filter
				}
				return out
			}
		}
	case sshURLRE.MatchString(base):
		m := sshURLRE.FindStringSubmatch(base)
		s.repo, s.owner, s.name = base, m[1], m[2]
	case shorthandRE.MatchString(base):
		m := shorthandRE.FindStringSubmatch(base)
		s.owner, s.name = m[1], m[2]
		// With GH_HOST, the CLI reads a shorthand as a repo on that host.
		if h := strings.TrimSpace(os.Getenv("GH_HOST")); h == "" || strings.EqualFold(h, "github.com") {
			s.repo = "https://github.com/" + m[1] + "/" + m[2]
		}
		// The CLI reads @x as a ref only when x is a version or a full hash
		// and the source has no fragment. Else @x is a skill filter.
		if m[4] != "" && !hasFrag && versionPinRE.MatchString(m[4]) {
			s.ref = m[4]
			s.with = withFragment(m[1] + "/" + m[2])
		}
	}
	s.owner, s.name = strings.ToLower(s.owner), strings.ToLower(strings.TrimSuffix(s.name, ".git"))
	return s
}

// NPXSourceRef returns the ref that an npx source carries, or empty.
func NPXSourceRef(src string) string { return parseNPXSource(src).ref }

// snapshotOwners are the GitHub owners and repos for which the `skills` CLI
// 1.9.0 downloads a snapshot instead of a clone when no ref is given. A
// snapshot loses the executable bit, so its tree hash differs from a clone.
var snapshotOwners = map[string]bool{"vercel": true, "vercel-labs": true, "heygen-com": true, "remotion-dev": true}
var snapshotRepos = map[string]bool{"zapier/connectors": true}

func (s npxSource) snapshot() bool {
	return snapshotOwners[s.owner] || snapshotRepos[s.owner+"/"+s.name]
}

// encodeFrag encodes a ref as encodeURIComponent does, so that the CLI reads an
// @ or a # in the ref as part of the ref.
func encodeFrag(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(hexByte(c)))
		}
	}
	return b.String()
}

func hexByte(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&15]})
}

func decodeFrag(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}
