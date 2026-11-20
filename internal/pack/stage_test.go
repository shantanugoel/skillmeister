package pack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwapKeepRollbackAndFailureReporting(t *testing.T) {
	t.Run("rolls back old pack when staged rename fails", func(t *testing.T) {
		libDir := t.TempDir()
		writeTestFile(t, filepath.Join(libDir, "packs", "p", "s", "SKILL.md"), "old", 0o644)
		root, err := os.OpenRoot(libDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()

		_, err = SwapKeep(root, &Staged{Dir: ".import/missing/pack"}, "p")
		if err == nil {
			t.Fatal("SwapKeep must fail when staged dir is missing")
		}
		got, err := root.ReadFile("packs/p/s/SKILL.md")
		if err != nil || string(got) != "old" {
			t.Fatalf("old pack was not restored: %q %v", got, err)
		}
	})

	t.Run("preserves both errors and tombstone path when rollback fails", func(t *testing.T) {
		libDir := t.TempDir()
		writeTestFile(t, filepath.Join(libDir, "packs", "p", "s", "SKILL.md"), "old", 0o644)
		root, err := os.OpenRoot(libDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()

		stageErr := errors.New("staged rename failed")
		rollbackErr := errors.New("rollback rename failed")
		calls := 0
		_, err = swapKeep(root, &Staged{Dir: ".import/new/pack"}, "p", func(oldname, newname string) error {
			calls++
			switch calls {
			case 1:
				return root.Rename(oldname, newname) // dest -> tomb succeeds
			case 2:
				return stageErr // staged -> dest fails
			case 3:
				return rollbackErr // tomb -> dest fails
			default:
				t.Fatalf("unexpected rename call %d", calls)
				return nil
			}
		})
		if err == nil {
			t.Fatal("swapKeep must return an error")
		}
		if !errors.Is(err, stageErr) {
			t.Errorf("error %q does not wrap stageErr", err)
		}
		for _, want := range []string{rollbackErr.Error(), ".removed/.swap-p-"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
		// RecoverTombstones can still recover the stranded old pack.
		recovered, rerr := RecoverTombstones(root, func(name string) bool { return name == "p" })
		if rerr != nil || len(recovered) != 1 || recovered[0] != "p" {
			t.Fatalf("RecoverTombstones = %v, %v", recovered, rerr)
		}
		got, rerr := root.ReadFile("packs/p/s/SKILL.md")
		if rerr != nil || string(got) != "old" {
			t.Fatalf("recovered pack = %q, %v", got, rerr)
		}
	})
}

func TestConfineRejectsSiblingPrefix(t *testing.T) {
	libDir := t.TempDir()
	// Both a/b/c and ab/c exist: old prefix-trimming turned "ab/c" minus "a"
	// into "b/c", which matched a/b/c inside base "a".
	writeTestFile(t, filepath.Join(libDir, "a", "b", "c", "SKILL.md"), "inside", 0o644)
	writeTestFile(t, filepath.Join(libDir, "ab", "c", "SKILL.md"), "sibling", 0o644)

	root, err := os.OpenRoot(libDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := confine(root, "a", "a"); err != nil {
		t.Errorf("confine(a, a) failed: %v", err)
	}
	if err := confine(root, "a", "a/b/c"); err != nil {
		t.Errorf("confine(a, a/b/c) failed: %v", err)
	}
	if err := confine(root, "a", "ab/c"); err == nil {
		t.Fatal("confine(a, ab/c) accepted a sibling sharing the base prefix")
	}
}
