// Package manifest parses and validates skillmeister.yaml.
package manifest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// FileName is the manifest file name at the library root.
const FileName = "skillmeister.yaml"

// OwnPack is the reserved pack ID for the user's own skills.
const OwnPack = "own"

// OwnPath is the fixed directory of the own pack.
const OwnPath = "skills"

// DefaultSearchLimit is used when neither --limit nor defaults.search_limit is set.
const DefaultSearchLimit = 8

// maxManifestSize bounds the manifest read.
const maxManifestSize = 1 << 20

// Fetch is the single fetch mechanism of a pack.
type Fetch string

// Fetch values.
const (
	FetchLocal Fetch = "local"
	FetchNPX   Fetch = "npx"
	FetchGit   Fetch = "git"
)

// Trust is a declared trust level.
type Trust string

// Trust values. Modified is never declared; only doctor computes it.
const (
	Trusted   Trust = "trusted"
	Pinned    Trust = "pinned"
	Untrusted Trust = "untrusted"
)

// Rank orders trust levels for tie-breaks: lower is more trusted.
func (t Trust) Rank() int {
	switch t {
	case Trusted:
		return 0
	case Pinned:
		return 1
	default:
		return 2
	}
}

// NameRE matches a normalized pack ID or skill name.
var NameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidPackID reports whether id can name a pack that `add` creates.
func ValidPackID(id string) error {
	switch id {
	case OwnPack, ".import", ".removed", ".", "..":
		return fmt.Errorf("pack ID %q is reserved", id)
	}
	if !NameRE.MatchString(id) {
		return fmt.Errorf("pack ID %q must match %s", id, NameRE)
	}
	return nil
}

// Pack is one source of skills.
type Pack struct {
	Name    string   `yaml:"-"`
	Fetch   Fetch    `yaml:"fetch"`
	Source  string   `yaml:"source"`
	URL     string   `yaml:"url"`
	Ref     string   `yaml:"ref"`
	Path    string   `yaml:"path"`
	Trust   Trust    `yaml:"trust"`
	Enabled *bool    `yaml:"enabled"`
	Disable []string `yaml:"disable"`
}

// IsEnabled reports whether the pack is enabled. The default is true.
func (p *Pack) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// SkillDisabled reports whether the bare skill name is in the pack's disable list.
func (p *Pack) SkillDisabled(name string) bool { return slices.Contains(p.Disable, name) }

// Dir is the slash-path of the pack's skill directory relative to the library root.
func (p *Pack) Dir() string {
	if p.Name == OwnPack {
		return OwnPath
	}
	return "packs/" + p.Name
}

// Defaults holds the defaults block.
type Defaults struct {
	SearchLimit int `yaml:"search_limit"`
}

// Manifest is the parsed skillmeister.yaml.
type Manifest struct {
	Version  int                 `yaml:"version"`
	Packs    map[string]*Pack    `yaml:"packs"`
	Profiles map[string][]string `yaml:"profiles"`
	Defaults Defaults            `yaml:"defaults"`

	src  []byte
	file *ast.File
}

// Error is a manifest error with a position.
type Error struct {
	File string
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Msg)
}

// Load reads and validates the manifest at the root of the library.
func Load(root *os.Root) (*Manifest, error) {
	f, err := root.Open(FileName)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", FileName, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifestSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	if len(data) > maxManifestSize {
		return nil, &Error{File: FileName, Msg: "file is larger than 1 MB"}
	}
	return Parse(FileName, data)
}

// Parse parses and validates manifest bytes. name is used in error messages.
func Parse(name string, data []byte) (*Manifest, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, &Error{File: name, Msg: yamlMessage(err)}
	}
	if len(file.Docs) > 1 {
		return nil, &Error{File: name, Line: docLine(file.Docs[1]), Msg: "multiple YAML documents"}
	}
	if len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return nil, &Error{File: name, Msg: "manifest is empty"}
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict())
	if err := dec.Decode(&m); err != nil {
		return nil, &Error{File: name, Msg: yamlMessage(err)}
	}
	m.src = data
	m.file = file
	if err := m.validate(name); err != nil {
		return nil, err
	}
	return &m, nil
}

func docLine(d *ast.DocumentNode) int {
	if d != nil && d.Start != nil {
		return d.Start.Position.Line
	}
	return 0
}

// yamlMessage reduces a go-yaml error to its first message line, which carries
// the [line:column] position.
func yamlMessage(err error) string {
	s := yaml.FormatError(err, false, false)
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// lineOf returns the line of the node at the YAML path, or 0.
func (m *Manifest) lineOf(path string) int {
	p, err := yaml.PathString(path)
	if err != nil {
		return 0
	}
	n, err := p.FilterFile(m.file)
	if err != nil || n == nil {
		return 0
	}
	if t := n.GetToken(); t != nil {
		return t.Position.Line
	}
	return 0
}

// lineOfKey returns the line of key inside the mapping at parentPath, or 0.
func (m *Manifest) lineOfKey(parentPath, key string) int {
	p, err := yaml.PathString(parentPath)
	if err != nil {
		return 0
	}
	n, err := p.FilterFile(m.file)
	if err != nil || n == nil {
		return 0
	}
	var values []*ast.MappingValueNode
	switch v := n.(type) {
	case *ast.MappingNode:
		values = v.Values
	case *ast.MappingValueNode:
		values = []*ast.MappingValueNode{v}
	}
	for _, mv := range values {
		if mv.Key != nil && mv.Key.GetToken() != nil && mv.Key.GetToken().Value == key {
			return mv.Key.GetToken().Position.Line
		}
	}
	return 0
}

func (m *Manifest) validate(file string) error {
	fail := func(path, format string, args ...any) error {
		line := m.lineOf(path)
		return &Error{File: file, Line: line, Msg: fmt.Sprintf(format, args...)}
	}
	// failKey reports an error at the key of a pack or profile.
	failKey := func(parent, key, format string, args ...any) error {
		return &Error{File: file, Line: m.lineOfKey(parent, key), Msg: fmt.Sprintf(format, args...)}
	}
	if m.Version == 0 {
		return fail("$", "version is required")
	}
	if m.Version != 1 {
		return fail("$.version", "unsupported version %d (this binary reads version 1)", m.Version)
	}
	for _, name := range m.PackNames() {
		p := m.Packs[name]
		if p == nil {
			return failKey("$.packs", name, "pack %q is empty", name)
		}
		p.Name = name
		base := "$.packs." + quoteKey(name)
		if name == "." || name == ".." || name == ".import" || name == ".removed" || !NameRE.MatchString(name) {
			return failKey("$.packs", name, "invalid pack ID %q (must match %s)", name, NameRE)
		}
		switch p.Fetch {
		case FetchLocal, FetchNPX, FetchGit:
		case "":
			return failKey("$.packs", name, "pack %q: fetch is required (local, npx, or git)", name)
		default:
			return fail(base+".fetch", "pack %q: unknown fetch %q (want local, npx, or git)", name, p.Fetch)
		}
		switch p.Trust {
		case "":
			p.Trust = Untrusted
		case Trusted, Pinned, Untrusted:
		default:
			return fail(base+".trust", "pack %q: unknown trust %q (want trusted, pinned, or untrusted)", name, p.Trust)
		}
		if p.Trust == Pinned && p.Fetch == FetchLocal {
			return fail(base+".trust", "pack %q: pinned is not valid for a local pack (it has no lock entry)", name)
		}
		switch p.Fetch {
		case FetchNPX:
			if p.Source == "" {
				return failKey("$.packs", name, "pack %q: fetch npx needs source", name)
			}
			if p.URL != "" {
				return fail(base+".url", "pack %q: url is only valid for fetch git", name)
			}
		case FetchGit:
			if p.URL == "" {
				return failKey("$.packs", name, "pack %q: fetch git needs url", name)
			}
			if p.Source != "" {
				return fail(base+".source", "pack %q: source is only valid for fetch npx", name)
			}
		case FetchLocal:
			if p.Source != "" || p.URL != "" {
				return failKey("$.packs", name, "pack %q: source and url are not valid for fetch local", name)
			}
			if p.Ref != "" {
				return fail(base+".ref", "pack %q: ref is not valid for fetch local", name)
			}
		}
		if name == OwnPack {
			if p.Fetch != FetchLocal {
				return fail(base+".fetch", "pack %q must use fetch local", name)
			}
			if p.Path == "" {
				p.Path = OwnPath
			}
			if p.Path != OwnPath {
				return fail(base+".path", "pack %q path must be %q", name, OwnPath)
			}
		}
		for i, s := range p.Disable {
			if !NameRE.MatchString(s) {
				return fail(fmt.Sprintf("%s.disable[%d]", base, i),
					"pack %q: disable entry %q must be a bare normalized skill name", name, s)
			}
		}
	}
	for _, prof := range m.ProfileNames() {
		path := "$.profiles." + quoteKey(prof)
		if !NameRE.MatchString(prof) {
			return failKey("$.profiles", prof, "invalid profile name %q", prof)
		}
		for i, pk := range m.Profiles[prof] {
			if m.Packs[pk] == nil {
				return fail(fmt.Sprintf("%s[%d]", path, i), "profile %q lists unknown pack %q", prof, pk)
			}
		}
	}
	if m.Defaults.SearchLimit < 0 {
		return fail("$.defaults.search_limit", "search_limit must be positive")
	}
	return nil
}

func quoteKey(k string) string {
	return "'" + strings.ReplaceAll(k, "'", `\'`) + "'"
}

// PackNames returns pack IDs in sorted order.
func (m *Manifest) PackNames() []string {
	names := make([]string, 0, len(m.Packs))
	for n := range m.Packs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ProfileNames returns profile names in sorted order.
func (m *Manifest) ProfileNames() []string {
	names := make([]string, 0, len(m.Profiles))
	for n := range m.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SearchLimit returns defaults.search_limit or the built-in default.
func (m *Manifest) SearchLimit() int {
	if m.Defaults.SearchLimit > 0 {
		return m.Defaults.SearchLimit
	}
	return DefaultSearchLimit
}

// Profile returns the pack set of a profile.
func (m *Manifest) Profile(name string) ([]string, error) {
	packs, ok := m.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (known: %s)", name, strings.Join(m.ProfileNames(), ", "))
	}
	return packs, nil
}
