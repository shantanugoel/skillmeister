package lock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
)

// FileName is the router lockfile at the library root.
const FileName = "skillmeister.lock.json"

// Entry pins one fetched pack.
type Entry struct {
	Fetch string `json:"fetch"`
	// Resolved is the full commit that the pack ref pointed to at fetch time.
	Resolved string `json:"resolved"`
	// Tree is the hash of packs/<pack>/ after the copy.
	Tree string `json:"tree"`
}

// File is the lockfile content.
type File struct {
	Version int              `json:"version"`
	Packs   map[string]Entry `json:"packs"`
}

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	treeRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// New returns an empty lockfile.
func New() *File { return &File{Version: 1, Packs: map[string]Entry{}} }

// Read reads the lockfile. A missing file is an empty lockfile.
func Read(root *os.Root) (*File, error) {
	data, err := root.ReadFile(FileName)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", FileName, f.Version)
	}
	if f.Packs == nil {
		f.Packs = map[string]Entry{}
	}
	for name, e := range f.Packs {
		if e.Fetch != "npx" && e.Fetch != "git" {
			return nil, fmt.Errorf("%s: pack %q: fetch must be npx or git", FileName, name)
		}
		if !commitRE.MatchString(e.Resolved) {
			return nil, fmt.Errorf("%s: pack %q: resolved is not a commit hash", FileName, name)
		}
		if !treeRE.MatchString(e.Tree) {
			return nil, fmt.Errorf("%s: pack %q: tree is not a sha256 hash", FileName, name)
		}
	}
	return &f, nil
}

// Marshal returns the canonical bytes of the lockfile: sorted keys, two-space
// indent, one trailing newline.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write writes the lockfile through a temp file and a rename, so a crash never
// leaves a half-written file.
func (f *File) Write(root *os.Root) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	return WriteFileAtomic(root, FileName, data, 0o644)
}

// WriteFileAtomic writes name inside root through a temp file and a rename.
func WriteFileAtomic(root *os.Root, name string, data []byte, perm os.FileMode) error {
	tmp := name + ".tmp"
	if err := root.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := root.Rename(tmp, name); err != nil {
		root.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}
