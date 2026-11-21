package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// Doc edits skillmeister.yaml through its YAML node tree, so comments and key
// order survive. Every change is validated by a full re-parse before it is
// written.
type Doc struct {
	name string
	file *ast.File
}

// OpenDoc parses the manifest at the library root for editing. The manifest
// must already be valid.
func OpenDoc(root *os.Root) (*Doc, error) {
	data, err := root.ReadFile(FileName)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	return ParseDoc(FileName, data)
}

// ParseDoc parses manifest bytes for editing.
func ParseDoc(name string, data []byte) (*Doc, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if _, err := Parse(name, data); err != nil {
		return nil, err
	}
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, yamlError(name, err)
	}
	return &Doc{name: name, file: file}, nil
}

// Bytes returns the edited manifest.
func (d *Doc) Bytes() []byte {
	s := d.file.String()
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s)
}

// Manifest re-parses the edited bytes and returns the validated manifest.
func (d *Doc) Manifest() (*Manifest, error) { return Parse(d.name, d.Bytes()) }

// Save validates the edit and writes it atomically through a temp file.
func (d *Doc) Save(root *os.Root) error {
	if _, err := d.Manifest(); err != nil {
		return fmt.Errorf("edit would make the manifest invalid: %w", err)
	}
	tmp := FileName + ".tmp"
	if err := root.WriteFile(tmp, d.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := root.Rename(tmp, FileName); err != nil {
		root.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

func (d *Doc) node(path string) (ast.Node, error) {
	p, err := yaml.PathString(path)
	if err != nil {
		return nil, err
	}
	n, err := p.FilterFile(d.file)
	if err != nil {
		return nil, nil // not found
	}
	return n, nil
}

func scalar(s string) (string, error) {
	b, err := yaml.Marshal(s)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// set replaces the value at path, or adds key to the mapping at parent.
func (d *Doc) set(parent, key, valueYAML string) error {
	path := parent + "." + key
	n, err := d.node(path)
	if err != nil {
		return err
	}
	p, err := yaml.PathString(path)
	if err != nil {
		return err
	}
	if n != nil {
		// A replaced node loses its trailing comment, so carry it over.
		cg := n.GetComment()
		if err := p.ReplaceWithReader(d.file, strings.NewReader(valueYAML)); err != nil {
			return err
		}
		if cg != nil {
			if nn, _ := d.node(path); nn != nil {
				nn.SetComment(cg)
			}
		}
		return nil
	}
	pp, err := yaml.PathString(parent)
	if err != nil {
		return err
	}
	return pp.MergeFromReader(d.file, strings.NewReader(key+": "+valueYAML+"\n"))
}

func packPath(pack string) string { return "$.packs." + quoteKey(pack) }

func (d *Doc) requirePack(pack string) error {
	n, err := d.node(packPath(pack))
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("pack %q is not in %s", pack, d.name)
	}
	return nil
}

// SetEnabled sets enabled for a pack.
func (d *Doc) SetEnabled(pack string, enabled bool) error {
	if err := d.requirePack(pack); err != nil {
		return err
	}
	return d.set(packPath(pack), "enabled", fmt.Sprint(enabled))
}

// SetTrust sets trust for a pack.
func (d *Doc) SetTrust(pack string, t Trust) error {
	if err := d.requirePack(pack); err != nil {
		return err
	}
	return d.set(packPath(pack), "trust", string(t))
}

// SetField sets a string field (`ref`, `path`, `source`, `url`) of a pack.
func (d *Doc) SetField(pack, field, value string) error {
	switch field {
	case "ref", "path", "source", "url":
	default:
		return fmt.Errorf("field %q is not editable", field)
	}
	if err := d.requirePack(pack); err != nil {
		return err
	}
	v, err := scalar(value)
	if err != nil {
		return err
	}
	return d.set(packPath(pack), field, v)
}

// ClearField removes a string field (`ref` or `path`) of a pack.
func (d *Doc) ClearField(pack, field string) error {
	switch field {
	case "ref", "path":
	default:
		return fmt.Errorf("field %q cannot be removed", field)
	}
	if err := d.requirePack(pack); err != nil {
		return err
	}
	return d.removeKey(packPath(pack), field)
}

// SetDisable writes the disable list of a pack. An empty list removes the key.
func (d *Doc) SetDisable(pack string, names []string) error {
	if err := d.requirePack(pack); err != nil {
		return err
	}
	if len(names) == 0 {
		return d.removeKey(packPath(pack), "disable")
	}
	return d.set(packPath(pack), "disable", "["+strings.Join(names, ", ")+"]")
}

// AddPack appends a new pack to the packs mapping.
func (d *Doc) AddPack(name string, p *Pack) error {
	if err := ValidPackID(name); err != nil {
		return err
	}
	if n, _ := d.node(packPath(name)); n != nil {
		return fmt.Errorf("pack %q already exists", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n  fetch: %s\n", name, p.Fetch)
	add := func(k, v string) error {
		if v == "" {
			return nil
		}
		sv, err := scalar(v)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "  %s: %s\n", k, sv)
		return nil
	}
	for _, kv := range [][2]string{{"source", p.Source}, {"url", p.URL}, {"ref", p.Ref}, {"path", p.Path}} {
		if err := add(kv[0], kv[1]); err != nil {
			return err
		}
	}
	trust := p.Trust
	if trust == "" {
		trust = Untrusted
	}
	fmt.Fprintf(&b, "  trust: %s\n  enabled: true\n", trust)

	packs, err := d.node("$.packs")
	if err != nil {
		return err
	}
	if mn, ok := packs.(*ast.MappingNode); ok && len(mn.Values) > 0 {
		pp, _ := yaml.PathString("$.packs")
		return pp.MergeFromReader(d.file, strings.NewReader(b.String()))
	}
	// There is no block mapping to merge into: replace it with a new one.
	if packs != nil {
		if err := d.removeKey("$", "packs"); err != nil {
			return err
		}
	}
	root, _ := yaml.PathString("$")
	block := "packs:\n  " + strings.ReplaceAll(strings.TrimRight(b.String(), "\n"), "\n", "\n  ") + "\n"
	return root.MergeFromReader(d.file, strings.NewReader(block))
}

// RemovePack deletes a pack and drops it from every profile. It returns the
// profiles that changed.
func (d *Doc) RemovePack(name string) ([]string, error) {
	if err := d.requirePack(name); err != nil {
		return nil, err
	}
	if err := d.removeKey("$.packs", name); err != nil {
		return nil, err
	}
	if n, _ := d.node("$.packs"); n != nil {
		if mn, ok := n.(*ast.MappingNode); ok && len(mn.Values) == 0 {
			if err := d.replace("$.packs", "{}"); err != nil {
				return nil, err
			}
		}
	}
	return d.dropFromProfiles(name)
}

func (d *Doc) replace(path, valueYAML string) error {
	p, err := yaml.PathString(path)
	if err != nil {
		return err
	}
	return p.ReplaceWithReader(d.file, strings.NewReader(valueYAML))
}

// dropFromProfiles removes name from every profile list that names it.
func (d *Doc) dropFromProfiles(name string) ([]string, error) {
	// Parse the current text without validation to read the profiles.
	var raw struct {
		Profiles map[string][]string `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(d.Bytes(), &raw); err != nil {
		return nil, err
	}
	var changed []string
	for prof, packs := range raw.Profiles {
		var kept []string
		for _, p := range packs {
			if p != name {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(packs) {
			continue
		}
		changed = append(changed, prof)
		if err := d.replace("$.profiles."+quoteKey(prof), "["+strings.Join(kept, ", ")+"]"); err != nil {
			return nil, err
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// removeKey deletes key from the mapping at parent.
func (d *Doc) removeKey(parent, key string) error {
	pp, err := yaml.PathString(parent)
	if err != nil {
		return err
	}
	n, err := pp.FilterFile(d.file)
	if err != nil {
		return nil // parent missing: nothing to remove
	}
	switch v := n.(type) {
	case *ast.MappingNode:
		for i, mv := range v.Values {
			if mv.Key != nil && mv.Key.GetToken() != nil && mv.Key.GetToken().Value == key {
				v.Values = append(v.Values[:i], v.Values[i+1:]...)
				return nil
			}
		}
	case *ast.MappingValueNode:
		if v.Key != nil && v.Key.GetToken() != nil && v.Key.GetToken().Value == key {
			return errors.New("cannot remove the only key of a mapping in place")
		}
	}
	return nil
}
