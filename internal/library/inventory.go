package library

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Inventory bounds.
const (
	maxInventoryEntries = 500
	maxInventoryDepth   = 8
)

// File is one entry in a skill's file inventory.
type File struct {
	// Path is relative to the skill directory, with slashes.
	Path    string
	Size    int64
	Script  bool
	Symlink bool
}

var scriptExt = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".py": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".rb": true,
	".pl": true, ".php": true, ".ps1": true, ".bat": true, ".cmd": true,
}

// Inventory lists the files under a skill directory. Directories that are
// symlinks are listed, not followed. All reads go through the library root.
func (l *Library) Inventory(r *Record) ([]File, error) {
	var out []File
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		entries, err := l.readDir(path.Join(r.Dir, rel))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if len(out) >= maxInventoryEntries {
				return nil
			}
			p := path.Join(rel, e.Name())
			if rel == "" {
				p = e.Name()
			}
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				out = append(out, File{Path: p, Symlink: true})
			case e.IsDir():
				if depth+1 > maxInventoryDepth {
					continue
				}
				if err := walk(p, depth+1); err != nil {
					return err
				}
			case e.Type().IsRegular():
				info, err := e.Info()
				if err != nil {
					return fmt.Errorf("stat %s: %w", p, err)
				}
				out = append(out, File{
					Path:   p,
					Size:   info.Size(),
					Script: isScript(p, info.Mode()),
				})
			}
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func isScript(p string, mode fs.FileMode) bool {
	if mode&0o111 != 0 {
		return true
	}
	if scriptExt[strings.ToLower(path.Ext(p))] {
		return true
	}
	return strings.HasPrefix(p, "scripts/") && !strings.EqualFold(path.Ext(p), ".md")
}
