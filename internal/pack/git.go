package pack

import (
	"fmt"
	"os"
	"path"
	"strings"
)

// fetchGit clones spec.URL at spec.Ref and stages the skills of the clone. It
// reads the skills from spec.Path, or from the clone root, or from skills/.
func fetchGit(root *os.Root, work string, spec Spec) (*Staged, error) {
	if err := checkArg("git url", spec.URL); err != nil {
		return nil, err
	}
	if err := checkArg("ref", spec.Ref); err != nil {
		return nil, err
	}
	repo := path.Join(work, "repo")
	if err := root.Mkdir(repo, 0o755); err != nil {
		return nil, err
	}
	abs := root.Name() + "/" + repo
	if _, err := run(abs, nil, "git", "init", "-q"); err != nil {
		return nil, err
	}
	if _, err := run(abs, nil, "git", "remote", "add", "origin", spec.URL); err != nil {
		return nil, err
	}
	ref := spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	// A shallow fetch is fast. Some servers refuse a fetch by commit, so a
	// full fetch is the fallback.
	if _, err := run(abs, nil, "git", "fetch", "-q", "--depth", "1", "origin", ref); err == nil {
		if _, err := run(abs, nil, "git", "checkout", "-q", "--detach", "FETCH_HEAD"); err != nil {
			return nil, err
		}
	} else {
		if _, err := run(abs, nil, "git", "fetch", "-q", "--tags", "origin"); err != nil {
			return nil, fmt.Errorf("fetch %s: %w", spec.URL, err)
		}
		if _, err := run(abs, nil, "git", "checkout", "-q", "--detach", ref); err != nil {
			return nil, fmt.Errorf("checkout %s: %w", ref, err)
		}
	}
	head, err := run(abs, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	resolved := strings.TrimSpace(head)

	sub := strings.Trim(path.Clean("/"+spec.Path), "/")
	candidates := []string{sub}
	if spec.Path == "" {
		candidates = []string{"", "skills"}
	}
	for _, c := range candidates {
		dir := repo
		if c != "" {
			dir = path.Join(repo, c)
		}
		if err := confine(root, repo, dir); err != nil {
			if spec.Path != "" {
				return nil, err
			}
			continue
		}
		if !hasSkills(root, dir) {
			continue
		}
		s, err := normalizeMove(root, work, repo, dir)
		if err != nil {
			return nil, err
		}
		s.Resolved = resolved
		s.Path = c
		return s, nil
	}
	return nil, fmt.Errorf("%s: %w (looked in %s)", spec.URL, errNoSkills, strings.Join(quoteAll(candidates), ", "))
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		if s == "" {
			s = "."
		}
		out[i] = "\"" + s + "\""
	}
	return out
}
