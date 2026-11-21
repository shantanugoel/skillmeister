package pack

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// SkillsLockName is the lockfile of the `skills` CLI at the library root.
const SkillsLockName = "skills-lock.json"

// skillsLock is the part of skills-lock.json that skillmeister reads. Entries
// stay raw, so unknown fields survive a merge.
type skillsLock struct {
	Version int                        `json:"version"`
	Skills  map[string]json.RawMessage `json:"skills"`
}

type skillsEntry struct {
	Commit string `json:"commit"`
}

// fetchNPX runs the pinned `skills` CLI in the staging directory. It installs
// every skill of spec.Source as a copy for one agent, then reads the commit from
// the lock entries that the CLI wrote.
//
// For a GitHub source, skillmeister first resolves the ref to a full commit
// with git ls-remote and gives the CLI that commit. Then add, update, and
// restore all fetch by commit, and so get the same files: without a ref, the
// CLI downloads a snapshot for some owners, and a snapshot loses the
// executable bit.
func fetchNPX(root *os.Root, work string, spec Spec) (*Staged, error) {
	if err := checkArg("source", spec.Source); err != nil {
		return nil, err
	}
	if err := checkArg("ref", spec.Ref); err != nil {
		return nil, err
	}
	bin, err := SkillsCLI(spec.Log)
	if err != nil {
		return nil, err
	}
	src := parseNPXSource(spec.Source)
	ref := normRef(spec.Ref)
	if ref == "" {
		ref = normRef(src.ref)
	}
	want := ""
	if fullSHARE.MatchString(ref) {
		want = ref
	} else if src.repo != "" {
		latest, err := LatestCommit(&manifest.Pack{Fetch: manifest.FetchGit, URL: src.repo, Ref: ref})
		switch {
		case err == nil:
			want = latest.Commit
		case errors.Is(err, ErrRefNotFound):
			return nil, err
		case src.snapshot() && ref == "":
			// Without a ref, the CLI downloads a snapshot for these owners.
			return nil, fmt.Errorf("cannot resolve the commit of %s: %w", spec.Source, err)
		}
		// On another failure, such as a private repo that only the gh CLI
		// can read, the CLI gets the ref as it is.
	}
	pkg := spec.Source
	switch {
	case want != "":
		pkg = src.with(want)
	case ref != "" && ref != src.ref:
		pkg = src.with(ref)
	}
	abs := filepath.Join(root.Name(), filepath.FromSlash(work))
	if _, err := runCmd(abs, []string{"DO_NOT_TRACK=1"}, true, bin, "add", pkg, "--skill", "*", "-a", "claude-code", "-y", "--copy"); err != nil {
		return nil, err
	}
	srcDir := path.Join(work, ".claude", "skills")
	lockData, err := root.ReadFile(path.Join(work, SkillsLockName))
	if err != nil {
		return nil, fmt.Errorf("the `skills` CLI wrote no %s: %w", SkillsLockName, err)
	}
	var lock skillsLock
	if err := json.Unmarshal(lockData, &lock); err != nil {
		return nil, fmt.Errorf("%s from the `skills` CLI: %w", SkillsLockName, err)
	}
	s, err := normalizeMove(root, work, work, srcDir)
	if err != nil {
		return nil, err
	}
	// The `skills` CLI keys its lock by the installed skill name, and so does the
	// root skills-lock.json. Two packs that hold a skill of the same name share
	// one key there; the later add wins. skillmeister.lock.json is the router
	// lock and is keyed by pack, so this does not affect drift checks.
	s.LockEntries = map[string][]byte{}
	commits := map[string]bool{}
	names := make([]string, 0, len(lock.Skills))
	for n := range lock.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var e skillsEntry
		if err := json.Unmarshal(lock.Skills[n], &e); err != nil {
			return nil, fmt.Errorf("%s: entry %q: %w", SkillsLockName, n, err)
		}
		if e.Commit != "" {
			commits[e.Commit] = true
		}
		s.LockEntries[n] = lock.Skills[n]
	}
	switch len(commits) {
	case 0:
		return nil, fmt.Errorf("%s from the `skills` CLI has no commit", SkillsLockName)
	case 1:
		for c := range commits {
			s.Resolved = c
		}
	default:
		return nil, fmt.Errorf("%s from the `skills` CLI lists more than one commit", SkillsLockName)
	}
	if want != "" && s.Resolved != want {
		return nil, fmt.Errorf("the `skills` CLI installed commit %s, not the requested commit %s", short(s.Resolved), short(want))
	}
	return s, nil
}

// MergeSkillsLock merges lock entries into the root skills-lock.json. Entries
// of the same name are replaced. Other entries and unknown fields stay.
func MergeSkillsLock(root *os.Root, entries map[string][]byte) error {
	if len(entries) == 0 {
		return nil
	}
	all := map[string]json.RawMessage{}
	skills := map[string]json.RawMessage{}
	data, err := root.ReadFile(SkillsLockName)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &all); err != nil {
			return fmt.Errorf("%s: %w", SkillsLockName, err)
		}
		if raw, ok := all["skills"]; ok {
			if err := json.Unmarshal(raw, &skills); err != nil {
				return fmt.Errorf("%s: skills: %w", SkillsLockName, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		all["version"] = json.RawMessage("2")
	default:
		return err
	}
	for n, raw := range entries {
		skills[n] = raw
	}
	raw, err := json.Marshal(skills)
	if err != nil {
		return err
	}
	all["skills"] = raw
	out, err := marshalIndent(all)
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(root, SkillsLockName, out, 0o644)
}

// DropSkillsLock removes the entries of one source from the root
// skills-lock.json. Entries of other sources stay. A missing file is not an
// error.
func DropSkillsLock(root *os.Root, source string) error {
	data, err := root.ReadFile(SkillsLockName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &all); err != nil {
		return fmt.Errorf("%s: %w", SkillsLockName, err)
	}
	skills := map[string]json.RawMessage{}
	if raw, ok := all["skills"]; ok {
		if err := json.Unmarshal(raw, &skills); err != nil {
			return fmt.Errorf("%s: skills: %w", SkillsLockName, err)
		}
	}
	changed := false
	for n, raw := range skills {
		var e struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Source == source {
			delete(skills, n)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	raw, err := json.Marshal(skills)
	if err != nil {
		return err
	}
	all["skills"] = raw
	out, err := marshalIndent(all)
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(root, SkillsLockName, out, 0o644)
}

func short(s string) string { return s[:min(len(s), 12)] }
