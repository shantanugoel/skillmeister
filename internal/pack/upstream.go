package pack

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/manifest"
)

var commitishRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// UpstreamURL returns the git URL that serves a fetched pack, or an error when
// the pack has none that skillmeister can query.
func UpstreamURL(p *manifest.Pack) (string, error) {
	switch p.Fetch {
	case manifest.FetchGit:
		return p.URL, nil
	case manifest.FetchNPX:
		src := p.Source
		if i := strings.IndexByte(src, '#'); i >= 0 {
			src = src[:i]
		}
		switch {
		case strings.HasPrefix(src, "https://github.com/"), strings.HasPrefix(src, "git@github.com:"):
			return src, nil
		case regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(src):
			return "https://github.com/" + src, nil
		}
		return "", fmt.Errorf("source %q is not a GitHub repository, so skillmeister cannot look up its newest commit", p.Source)
	}
	return "", fmt.Errorf("pack %s has no upstream", p.Name)
}

// Latest is the newest upstream commit for the ref of a pack.
type Latest struct {
	Commit string
	// Pinned is true when the ref is a commit, so nothing newer can exist.
	Pinned bool
}

// LatestCommit asks the upstream for the commit that the pack ref points to
// now. It only reads from the network.
func LatestCommit(p *manifest.Pack) (*Latest, error) {
	if commitishRE.MatchString(p.Ref) {
		return &Latest{Commit: p.Ref, Pinned: true}, nil
	}
	url, err := UpstreamURL(p)
	if err != nil {
		return nil, err
	}
	if err := checkArg("url", url); err != nil {
		return nil, err
	}
	if err := checkArg("ref", p.Ref); err != nil {
		return nil, err
	}
	patterns := []string{"HEAD"}
	if p.Ref != "" && p.Ref != "HEAD" {
		patterns = []string{"refs/heads/" + p.Ref, "refs/tags/" + p.Ref, "refs/tags/" + p.Ref + "^{}"}
	}
	out, err := run("", nil, "git", append([]string{"ls-remote", url}, patterns...)...)
	if err != nil {
		return nil, err
	}
	// A peeled tag (^{}) is the commit; a plain line is the commit or a tag object.
	var plain, peeled string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasSuffix(ref, "^{}") {
			peeled = sha
		} else if plain == "" {
			plain = sha
		}
	}
	switch {
	case peeled != "":
		return &Latest{Commit: peeled}, nil
	case plain != "":
		return &Latest{Commit: plain}, nil
	}
	return nil, fmt.Errorf("ref %q not found at %s", p.Ref, url)
}
