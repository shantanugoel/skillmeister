// Package library discovers skills in the packs of a skillmeister library.
//
// Every read goes through os.Root, so a symlink that points outside the library
// fails at runtime instead of leaking files.
package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/shantanugoel/skillmeister/internal/manifest"
)

// Scan bounds.
const (
	// MaxSkillFile is the largest SKILL.md that a scan reads.
	MaxSkillFile = 1 << 20
	// BodyIndexBytes is how much of a body is kept for body matching.
	BodyIndexBytes = 8 << 10
)

// SkillFile is the file name that marks a skill directory.
const SkillFile = "SKILL.md"

// scanWorkers bounds the parallel skill reads of a scan.
const scanWorkers = 8

// Library is an opened skillmeister library.
type Library struct {
	// Abs is the absolute path of the library root.
	Abs      string
	Root     *os.Root
	Manifest *manifest.Manifest
}

// ErrNotLibrary means that a directory is missing or has no manifest.
var ErrNotLibrary = errors.New("not a library")

// Open opens the library at dir and loads its manifest.
func Open(dir string) (*Library, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s is %w: the directory does not exist", abs, ErrNotLibrary)
	}
	if err != nil {
		return nil, fmt.Errorf("open library %s: %w", abs, err)
	}
	m, err := manifest.Load(root)
	if err != nil {
		root.Close()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s is %w: no %s", abs, ErrNotLibrary, manifest.FileName)
		}
		return nil, err
	}
	return &Library{Abs: abs, Root: root, Manifest: m}, nil
}

// Close releases the root handle.
func (l *Library) Close() error { return l.Root.Close() }

// Record is one discovered skill.
type Record struct {
	ID          string
	Name        string
	Pack        string
	Description string
	// Path is the absolute path of SKILL.md.
	Path string
	// Dir is the slash-path of the skill directory relative to the library root.
	Dir   string
	Trust manifest.Trust
	// Warnings lists native-only features that the skill uses. Never nil.
	Warnings []string
	// HasFrontmatter is false when the file has no frontmatter.
	HasFrontmatter bool
	// FrontmatterName is the name field of the frontmatter, if any.
	FrontmatterName string
	// Body holds the first BodyIndexBytes of the body.
	Body string
}

// Scope selects which packs a scan covers.
type Scope struct {
	// Profile restricts the scan to the packs of a profile. Empty means all.
	Profile string
	// Packs restricts the scan further to the listed packs.
	Packs []string
	// All includes disabled packs and skills.
	All bool
}

// PacksInScope resolves the profile and --pack filters to a sorted pack list.
func (l *Library) PacksInScope(s Scope) ([]*manifest.Pack, error) {
	m := l.Manifest
	allowed := map[string]bool{}
	if s.Profile != "" {
		members, err := m.Profile(s.Profile)
		if err != nil {
			return nil, err
		}
		for _, p := range members {
			allowed[p] = true
		}
	} else {
		for n := range m.Packs {
			allowed[n] = true
		}
	}
	if len(s.Packs) > 0 {
		for _, p := range s.Packs {
			if m.Packs[p] == nil {
				return nil, fmt.Errorf("unknown pack %q (known: %s)", p, strings.Join(m.PackNames(), ", "))
			}
		}
		for n := range allowed {
			if !slices.Contains(s.Packs, n) {
				delete(allowed, n)
			}
		}
	}
	var out []*manifest.Pack
	for _, n := range m.PackNames() {
		p := m.Packs[n]
		if !allowed[n] || (!s.All && !p.IsEnabled()) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Scan discovers skills in scope. Problems with single packs or skills are
// returned as warnings and do not stop the scan.
func (l *Library) Scan(s Scope) (recs []*Record, warnings []string, err error) {
	packs, err := l.PacksInScope(s)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range packs {
		r, w := l.ScanPack(p, s.All)
		recs = append(recs, r...)
		warnings = append(warnings, w...)
	}
	return recs, warnings, nil
}

// ScanPack discovers the skills of one pack. Only immediate subdirectories of
// the pack directory are skills.
func (l *Library) ScanPack(p *manifest.Pack, includeDisabled bool) (recs []*Record, warnings []string) {
	dir := p.Dir()
	entries, err := l.readDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, []string{fmt.Sprintf("pack %s: directory %s is missing", p.Name, dir)}
		}
		return nil, []string{fmt.Sprintf("pack %s: %v", p.Name, err)}
	}
	type job struct {
		name string
		rec  *Record
		err  error
	}
	var jobs []*job
	for _, e := range entries {
		// Symlinked directories are not followed; only real directories count.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if NormalizeName(name) != name || !manifest.NameRE.MatchString(name) {
			warnings = append(warnings, fmt.Sprintf("skipping %s/%s: directory name is not normalized", p.Name, name))
			continue
		}
		if !includeDisabled && p.SkillDisabled(name) {
			continue
		}
		jobs = append(jobs, &job{name: name})
	}
	// Read and parse skills in parallel. Results keep the sorted order.
	var wg sync.WaitGroup
	sem := make(chan struct{}, scanWorkers)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.rec, j.err = l.readRecord(p, j.name, path.Join(dir, j.name))
		}()
	}
	wg.Wait()
	for _, j := range jobs {
		switch {
		case j.err == nil:
			recs = append(recs, j.rec)
		case errors.Is(j.err, fs.ErrNotExist):
			// A directory without SKILL.md is not a skill.
		default:
			warnings = append(warnings, fmt.Sprintf("skipping %s/%s: %v", p.Name, j.name, j.err))
		}
	}
	return recs, warnings
}

func (l *Library) readDir(dir string) ([]fs.DirEntry, error) {
	f, err := l.Root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (l *Library) readRecord(p *manifest.Pack, name, skillDir string) (*Record, error) {
	file := path.Join(skillDir, SkillFile)
	data, err := l.readBounded(file)
	if err != nil {
		return nil, err
	}
	fm, has, body, err := ParseSkill(data)
	if err != nil {
		return nil, err
	}
	n := len(body)
	if n > BodyIndexBytes {
		n = BodyIndexBytes
	}
	return &Record{
		ID:              p.Name + "/" + name,
		Name:            name,
		Pack:            p.Name,
		Description:     fm.Description,
		Path:            filepath.Join(l.Abs, filepath.FromSlash(file)),
		Dir:             skillDir,
		Trust:           p.Trust,
		Warnings:        DetectWarnings(fm, body),
		HasFrontmatter:  has,
		FrontmatterName: fm.Name,
		Body:            string(body[:n]),
	}, nil
}

// readBounded reads a file through the root and rejects files over MaxSkillFile.
func (l *Library) readBounded(rel string) ([]byte, error) {
	f, err := l.Root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if st.Size() > MaxSkillFile {
		return nil, fmt.Errorf("%s is larger than 1 MB", path.Base(rel))
	}
	return io.ReadAll(io.LimitReader(f, MaxSkillFile+1))
}

// ReadSkill returns the SKILL.md bytes of a record, read through the root.
func (l *Library) ReadSkill(r *Record) ([]byte, error) {
	return l.readBounded(path.Join(r.Dir, SkillFile))
}

// NormalizeName lowercases a directory name and maps `_` and `.` to `-`.
func NormalizeName(dir string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '.' {
			return '-'
		}
		return r
	}, strings.ToLower(dir))
}

// Resolve finds a record by canonical ID (`pack/skill`) or bare name. For a
// bare name with several matches, the highest declared trust wins, then the own
// pack, then the pack name; the other matches are returned in also.
func Resolve(recs []*Record, ref string) (rec *Record, also []*Record, err error) {
	var matches []*Record
	if strings.Contains(ref, "/") {
		for _, r := range recs {
			if r.ID == ref {
				matches = append(matches, r)
			}
		}
	} else {
		for _, r := range recs {
			if r.Name == ref {
				matches = append(matches, r)
			}
		}
	}
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("no skill %q (it may be disabled; try `skillmeister list`)", ref)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Trust.Rank() != b.Trust.Rank() {
			return a.Trust.Rank() < b.Trust.Rank()
		}
		return Less(a, b)
	})
	return matches[0], matches[1:], nil
}

// SortRecords orders records for `list`: own pack first, then pack name, then
// skill name.
func SortRecords(recs []*Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if (a.Pack == manifest.OwnPack) != (b.Pack == manifest.OwnPack) {
			return a.Pack == manifest.OwnPack
		}
		if a.Pack != b.Pack {
			return a.Pack < b.Pack
		}
		return a.Name < b.Name
	})
}

// Less is the shared tie-break order: own pack first, then trust, then name.
func Less(a, b *Record) bool {
	if (a.Pack == manifest.OwnPack) != (b.Pack == manifest.OwnPack) {
		return a.Pack == manifest.OwnPack
	}
	if a.Trust.Rank() != b.Trust.Rank() {
		return a.Trust.Rank() < b.Trust.Rank()
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}
