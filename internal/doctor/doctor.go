// Package doctor checks a library for drift and structural problems. It only
// reads. It never executes anything.
package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/shantanugoel/skillmeister/internal/library"
	"github.com/shantanugoel/skillmeister/internal/lock"
	"github.com/shantanugoel/skillmeister/internal/manifest"
	"github.com/shantanugoel/skillmeister/internal/pack"
	"github.com/shantanugoel/skillmeister/internal/version"
)

// Severity of a finding. Only errors fail the check.
type Severity string

// Severities.
const (
	Error Severity = "error"
	Warn  Severity = "warn"
)

// Finding codes. Integrity codes are the ones that `sync --frozen` checks.
const (
	CodeMissingPack  = "missing-pack"
	CodeNoLockEntry  = "no-lock-entry"
	CodeLockMismatch = "lock-mismatch"
	CodeModified     = "modified"
	CodeOrphanPack   = "orphan-pack"
	CodeOrphanLock   = "orphan-lock"
	CodeTombstone    = "leftover-tombstone"
	CodeLeftover     = "leftover-import"
	CodeSkillName    = "skill-name"
	CodeUnparseable  = "unparseable"
	CodeNoFront      = "no-frontmatter"
	CodeNameMismatch = "name-mismatch"
	CodeNested       = "nested-skill"
	CodeNative       = "native-fields"
	CodeGitignore    = "gitignore-missing"
)

var integrity = map[string]bool{
	CodeMissingPack: true, CodeNoLockEntry: true, CodeLockMismatch: true, CodeModified: true,
	CodeOrphanPack: true, CodeOrphanLock: true, CodeTombstone: true,
}

// IsIntegrity reports whether a code is a drift or integrity code.
func IsIntegrity(code string) bool { return integrity[code] }

// Finding is one problem.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Pack     string   `json:"pack,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Message  string   `json:"message"`
}

// PackStatus summarizes one pack.
type PackStatus struct {
	Name     string         `json:"name"`
	Fetch    manifest.Fetch `json:"fetch"`
	Enabled  bool           `json:"enabled"`
	Declared manifest.Trust `json:"trust"`
	// Effective is the declared trust, or "modified" when a pinned pack drifted.
	Effective string `json:"effective_trust"`
	Skills    int    `json:"skills"`
	// Verified is true when the tree hash was compared with the lock.
	Verified bool `json:"verified"`
}

// Report is the result of a check.
type Report struct {
	Version  string       `json:"version"`
	Library  string       `json:"library"`
	Packs    []PackStatus `json:"packs"`
	Findings []Finding    `json:"findings"`
}

// Failed reports whether any finding is an error.
func (r *Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Severity == Error {
			return true
		}
	}
	return false
}

// IntegrityFindings returns the error findings that `sync --frozen` checks.
func (r *Report) IntegrityFindings() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == Error && IsIntegrity(f.Code) {
			out = append(out, f)
		}
	}
	return out
}

type checker struct {
	lib *library.Library
	rep *Report
	lk  *lock.File
}

func (c *checker) add(sev Severity, code, packName, subject, format string, args ...any) {
	c.rep.Findings = append(c.rep.Findings, Finding{
		Severity: sev, Code: code, Pack: packName, Subject: subject, Message: fmt.Sprintf(format, args...),
	})
}

// Run checks the whole library, including disabled packs and skills.
func Run(lib *library.Library) (*Report, error) {
	lk, err := lock.Read(lib.Root)
	if err != nil {
		return nil, err
	}
	c := &checker{lib: lib, lk: lk, rep: &Report{Version: version.String(), Library: lib.Abs}}
	m := lib.Manifest
	for _, name := range m.PackNames() {
		c.checkPack(m.Packs[name])
	}
	c.checkOrphans()
	c.checkGitignore()
	return c.rep, nil
}

// checkGitignore reports ignore lines that the library .gitignore lacks.
func (c *checker) checkGitignore() {
	missing, err := library.MissingGitignore(c.lib)
	if err != nil {
		c.add(Warn, CodeGitignore, "", library.GitignoreFile, "cannot read %s: %v", library.GitignoreFile, err)
		return
	}
	if len(missing) > 0 {
		c.add(Warn, CodeGitignore, "", library.GitignoreFile, "%s lacks %s; run `skillmeister init` to add them", library.GitignoreFile, strings.Join(missing, " "))
	}
}

func (c *checker) checkPack(p *manifest.Pack) {
	st := PackStatus{
		Name: p.Name, Fetch: p.Fetch, Enabled: p.IsEnabled(),
		Declared: p.Trust, Effective: string(p.Trust),
	}
	dir := p.Dir()
	entry, locked := c.lk.Packs[p.Name]
	present := true
	if _, err := c.lib.ReadDir(dir); err != nil {
		present = false
		switch {
		case p.Fetch == manifest.FetchLocal:
			c.add(Error, CodeMissingPack, p.Name, dir, "directory %s is missing; a local pack cannot be restored", dir)
		case locked:
			c.add(Error, CodeMissingPack, p.Name, dir, "directory %s is missing; run `skillmeister sync` to restore it", dir)
		default:
			c.add(Error, CodeMissingPack, p.Name, dir, "directory %s is missing and the pack has no lock entry", dir)
		}
	}
	if p.Fetch != manifest.FetchLocal {
		switch {
		case !locked:
			c.add(Error, CodeNoLockEntry, p.Name, "", "no entry in %s; re-add the pack with `add --force`", lock.FileName)
		case entry.Fetch != string(p.Fetch):
			c.add(Error, CodeLockMismatch, p.Name, "", "lock says fetch %s, manifest says %s", entry.Fetch, p.Fetch)
		case present && p.Trust == manifest.Pinned:
			st.Verified = true
			got, err := lock.HashTree(c.lib.Root, dir)
			switch {
			case err != nil:
				c.add(Error, CodeModified, p.Name, "", "cannot hash %s: %v", dir, err)
			case got != entry.Tree:
				st.Effective = "modified"
				c.add(Error, CodeModified, p.Name, "", "content differs from the lock (lock %s, found %s); review the change, then `trust --level pinned --accept` or `update --apply`", short(entry.Tree), short(got))
			}
		}
	}
	if present {
		st.Skills = c.checkSkills(p)
	}
	c.rep.Packs = append(c.rep.Packs, st)
}

func short(h string) string {
	h = strings.TrimPrefix(h, lock.HashPrefix)
	if len(h) > 12 {
		h = h[:12]
	}
	return h
}

// checkSkills inspects every immediate subdirectory of a pack and returns the
// number of valid skills.
func (c *checker) checkSkills(p *manifest.Pack) int {
	dir := p.Dir()
	entries, err := c.lib.ReadDir(dir)
	if err != nil {
		return 0
	}
	valid := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		skillDir := path.Join(dir, name)
		id := p.Name + "/" + name
		data, err := c.lib.ReadFileBounded(path.Join(skillDir, library.SkillFile))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				c.checkNested(p.Name, id, skillDir, 0)
				continue
			}
			c.add(Error, CodeUnparseable, p.Name, id, "%v", err)
			continue
		}
		if norm := library.NormalizeName(name); norm != name || !manifest.NameRE.MatchString(name) {
			c.add(Error, CodeSkillName, p.Name, id, "directory name is not normalized (expected %q); `find` cannot see this skill", norm)
			continue
		}
		fm, has, body, err := library.ParseSkill(data)
		if err != nil {
			c.add(Error, CodeUnparseable, p.Name, id, "%v", err)
			continue
		}
		valid++
		if !has {
			c.add(Warn, CodeNoFront, p.Name, id, "SKILL.md has no frontmatter; it has no description")
		} else if fm.Name != "" && fm.Name != name {
			c.add(Warn, CodeNameMismatch, p.Name, id, "frontmatter name %q differs from the directory name; the directory name is used", fm.Name)
		}
		if w := library.DetectWarnings(fm, body); len(w) > 0 {
			c.add(Warn, CodeNative, p.Name, id, "%s", strings.Join(w, "; "))
		}
		c.checkNested(p.Name, id, skillDir, 0)
	}
	return valid
}

// checkNested reports SKILL.md files below a skill directory. Only immediate
// subdirectories of a pack are skills, so a nested file is ignored by `find`.
func (c *checker) checkNested(packName, id, dir string, depth int) {
	if depth > 4 {
		return
	}
	entries, err := c.lib.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := path.Join(dir, e.Name())
		if _, err := c.lib.ReadFileBounded(path.Join(sub, library.SkillFile)); err == nil {
			c.add(Warn, CodeNested, packName, id, "%s/SKILL.md is nested; only immediate subdirectories of a pack are skills", strings.TrimPrefix(sub, path.Dir(dir)+"/"))
			continue
		}
		c.checkNested(packName, id, sub, depth+1)
	}
}

// checkOrphans finds directories and lock entries that nothing claims.
func (c *checker) checkOrphans() {
	m := c.lib.Manifest
	if entries, err := c.lib.ReadDir(pack.PacksDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if m.Packs[e.Name()] == nil {
				c.add(Error, CodeOrphanPack, e.Name(), path.Join(pack.PacksDir, e.Name()),
					"directory is not in the manifest; `sync` moves it to %s", pack.RemovedDir)
			}
		}
	}
	var names []string
	for n := range c.lk.Packs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if p := m.Packs[n]; p == nil || p.Fetch == manifest.FetchLocal {
			c.add(Error, CodeOrphanLock, n, "", "lock entry has no fetched pack in the manifest; `sync` drops it")
		}
	}
	if entries, err := c.lib.ReadDir(pack.RemovedDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), pack.SwapPrefix) {
				name, _ := pack.TombstonePack(e.Name())
				msg := "a crash left a swap tombstone; `sync` deletes it"
				if m.Packs[name] != nil && !pack.Exists(c.lib.Root, path.Join(pack.PacksDir, name)) {
					msg = "a crash left the old pack only here; `sync` puts it back"
				}
				c.add(Error, CodeTombstone, name, path.Join(pack.RemovedDir, e.Name()), "%s", msg)
			}
		}
	}
	if entries, err := c.lib.ReadDir(pack.ImportDir); err == nil && len(entries) > 0 {
		c.add(Warn, CodeLeftover, "", pack.ImportDir, "%d leftover staging entries; `sync` deletes them", len(entries))
	}
}
