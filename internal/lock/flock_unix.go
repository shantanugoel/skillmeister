//go:build unix

package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// LockName is the writer lock file at the library root. It is gitignored.
const LockName = ".skillmeister.lock"

// ErrBusy means that another writer holds the library lock.
var ErrBusy = errors.New("another skillmeister writer holds the library lock")

// Writer is a held exclusive lock on a library.
type Writer struct{ f *os.File }

// Acquire takes the exclusive writer lock without waiting. It returns ErrBusy
// when another process holds it. The OS drops the lock if the process dies.
func Acquire(root *os.Root) (*Writer, error) {
	f, err := root.OpenFile(LockName, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", LockName, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock %s: %w", LockName, err)
	}
	return &Writer{f: f}, nil
}

// Release drops the lock.
func (w *Writer) Release() {
	if w == nil || w.f == nil {
		return
	}
	syscall.Flock(int(w.f.Fd()), syscall.LOCK_UN)
	w.f.Close()
	w.f = nil
}
