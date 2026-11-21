package pack

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// fullSHARE matches a full commit hash. A shorter hex string can be a branch or
// a tag name, so it goes to the upstream like any other ref.
var fullSHARE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// anyCaseSHARE matches a full commit hash in any case.
var anyCaseSHARE = regexp.MustCompile(`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// normRef writes a full commit hash in lower case, as git and fullSHARE need.
func normRef(ref string) string {
	if anyCaseSHARE.MatchString(ref) {
		return strings.ToLower(ref)
	}
	return ref
}

// UpstreamURL returns the git URL that serves a fetched pack, or an error when
// the pack has none that skillmeister can query.
func UpstreamURL(p *manifest.Pack) (string, error) {
	switch p.Fetch {
	case manifest.FetchGit:
		return p.URL, nil
	case manifest.FetchNPX:
		if repo := parseNPXSource(p.Source).repo; repo != "" {
			return repo, nil
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

// ErrRefNotFound means that the upstream answered and does not have the ref.
var ErrRefNotFound = errors.New("ref not found")

// shortHashRE matches a hex string that is probably an abbreviated commit.
var shortHashRE = regexp.MustCompile(`^[0-9a-f]{7,39}$`)

// effectiveRef is the ref that a fetch of the pack uses: the manifest ref, or
// for an npx pack without one, the ref that its source carries.
func effectiveRef(p *manifest.Pack) string {
	if p.Ref == "" && p.Fetch == manifest.FetchNPX {
		return parseNPXSource(p.Source).ref
	}
	return p.Ref
}

// LatestCommit asks the upstream for the commit that the pack ref points to
// now. It only reads from the network.
func LatestCommit(p *manifest.Pack) (*Latest, error) {
	ref := normRef(effectiveRef(p))
	if fullSHARE.MatchString(ref) {
		return &Latest{Commit: ref, Pinned: true}, nil
	}
	url, err := UpstreamURL(p)
	if err != nil {
		return nil, err
	}
	if err := checkArg("url", url); err != nil {
		return nil, err
	}
	if err := checkArg("ref", ref); err != nil {
		return nil, err
	}
	patterns := []string{"HEAD"}
	if ref != "" && ref != "HEAD" {
		patterns = []string{"refs/heads/" + ref, "refs/tags/" + ref, "refs/tags/" + ref + "^{}"}
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
	if shortHashRE.MatchString(ref) {
		return nil, fmt.Errorf("%w: %q at %s; to pin a commit, give the full 40-character hash", ErrRefNotFound, ref, url)
	}
	if ref == "" {
		return nil, fmt.Errorf("%w: %s has no HEAD", ErrRefNotFound, url)
	}
	return nil, fmt.Errorf("%w: %q at %s", ErrRefNotFound, ref, url)
}
