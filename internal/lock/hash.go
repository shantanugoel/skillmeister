// Package lock reads and writes skillmeister.lock.json and computes the
// content hashes that it stores.
package lock

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
)

// HashPrefix starts every hash string.
const HashPrefix = "sha256:"

// excluded names are skipped at every depth.
var excluded = map[string]bool{".git": true, ".import": true, ".removed": true, ".DS_Store": true}

type entry struct {
	path    string // slash path relative to the hashed directory
	symlink bool
	exec    bool
	size    int64
}

// HashTree computes hash recipe v1 over the directory dir inside root.
//
// The input is the tag "v1", then for each entry in sorted slash-path order:
// a type tag (`f` for a file, `l` for a symlink), an executable byte, the
// length-prefixed path, and the length-prefixed content (for a symlink, the
// target). Lengths are 8-byte big-endian. Only regular files and symlinks are
// entries, so empty directories do not count. Symlinks are never followed.
func HashTree(root *os.Root, dir string) (string, error) {
	var entries []entry
	var walk func(rel string) error
	walk = func(rel string) error {
		f, err := root.Open(path.Join(dir, rel))
		if err != nil {
			return err
		}
		items, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, it := range items {
			if excluded[it.Name()] {
				continue
			}
			p := path.Join(rel, it.Name())
			switch {
			case it.Type()&fs.ModeSymlink != 0:
				entries = append(entries, entry{path: p, symlink: true})
			case it.IsDir():
				if err := walk(p); err != nil {
					return err
				}
			case it.Type().IsRegular():
				info, err := it.Info()
				if err != nil {
					return err
				}
				entries = append(entries, entry{path: p, exec: info.Mode()&0o111 != 0, size: info.Size()})
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	h := sha256.New()
	h.Write([]byte("v1"))
	for _, e := range entries {
		tag := byte('f')
		if e.symlink {
			tag = 'l'
		}
		var execByte byte
		if e.exec {
			execByte = 1
		}
		h.Write([]byte{tag, execByte})
		writeFrame(h, []byte(e.path))
		full := path.Join(dir, e.path)
		if e.symlink {
			target, err := root.Readlink(full)
			if err != nil {
				return "", err
			}
			writeFrame(h, []byte(target))
			continue
		}
		f, err := root.Open(full)
		if err != nil {
			return "", err
		}
		// Read the size from the open file, so the prefix matches the bytes.
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(info.Size()))
		h.Write(lenBuf[:])
		n, err := io.Copy(h, io.LimitReader(f, info.Size()))
		f.Close()
		if err != nil {
			return "", err
		}
		if n != info.Size() {
			return "", fmt.Errorf("%s changed while hashing", full)
		}
	}
	return HashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

func writeFrame(w io.Writer, b []byte) {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(b)))
	w.Write(l[:])
	w.Write(b)
}
