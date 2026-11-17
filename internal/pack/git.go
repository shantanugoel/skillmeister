package pack

import (
	"fmt"
	"os"
	"path"
	"strings"
)

// fetchGit clones spec.URL at spec.Ref and stages the skills of the clone. It
// reads the skills from spec.Path, or from the clone root, or from skills/.
// In each place, skill subdirectories win over a SKILL.md in the place itself,
// which makes the place one single skill.
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
		var s *Staged
		switch {
		case hasSkills(root, dir):
			s, err = normalizeMove(root, work, repo, dir)
		case hasSkillFile(root.FS(), dir):
			// Read the notes first: the move takes the directory away.
			var notes []string
			if c == "" {
				notes = rootNotes(root, repo)
			}
			s, err = singleMove(root, work, repo, dir, spec.Name)
			if err == nil {
				s.Notes = append(s.Notes, notes...)
			}
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		s.Resolved = resolved
		// The root is recorded as "." and not as "", so that a forced re-add
		// can replace an older path such as "skills".
		s.Path = c
		if c == "" {
			s.Path = "."
		}
		return s, nil
	}
	return nil, fmt.Errorf("%s: %w (looked in %s)", spec.URL, errNoSkills, strings.Join(quoteAll(candidates), ", "))
}

// rootNotes describes what a single skill at the clone root leaves out.
func rootNotes(root *os.Root, repo string) []string {
	var notes []string
	if n := countSkills(root.FS(), path.Join(repo, "skills")); n > 0 {
		notes = append(notes, fmt.Sprintf("the root SKILL.md is the skill; skills/ also has %d skills. Use --path skills to add those as their own pack", n))
	}
	if Exists(root, path.Join(repo, ".gitmodules")) {
		notes = append(notes, "the repo has git submodules; skillmeister does not fetch them, so the skill may be incomplete")
	}
	return notes
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
