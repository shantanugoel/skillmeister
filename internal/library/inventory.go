package library

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Inventory bounds. The walk stops at maxInventoryEntries files and reports
// that it did, so a listing never hides files without saying so.
const (
	maxInventoryEntries = 20000
	maxInventoryDepth   = 16
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
// truncated is true when the walk hit its bounds and the list is incomplete.
// On a traversal error, Inventory still returns the readable entries it found
// alongside the first error.
func (l *Library) Inventory(r *Record) (files []File, truncated bool, err error) {
	var out []File
	var firstErr error
	recordErr := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		entries, err := l.readDir(path.Join(r.Dir, rel))
		if err != nil {
			recordErr(err)
			return
		}
		for _, e := range entries {
			if len(out) >= maxInventoryEntries {
				truncated = true
				return
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
					truncated = true
					continue
				}
				walk(p, depth+1)
			case e.Type().IsRegular():
				info, err := e.Info()
				if err != nil {
					recordErr(fmt.Errorf("stat %s: %w", p, err))
					continue
				}
				out = append(out, File{
					Path:   p,
					Size:   info.Size(),
					Script: IsScript(p, info.Mode()),
				})
			}
		}
	}
	walk("", 0)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, truncated, firstErr
}

// IsScript reports whether a file path and mode look like a script.
func IsScript(p string, mode fs.FileMode) bool {
	if mode&0o111 != 0 {
		return true
	}
	if scriptExt[strings.ToLower(path.Ext(p))] {
		return true
	}
	return strings.HasPrefix(p, "scripts/") && !strings.EqualFold(path.Ext(p), ".md")
}
