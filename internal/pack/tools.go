package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shantanugoel/skillmeister"
)

// CacheEnv names the directory that holds the pinned `skills` CLI. It
// overrides the per-user cache directory.
const CacheEnv = "SKILLMEISTER_CACHE_DIR"

// skillsBinRel is the CLI inside its cache directory.
var skillsBinRel = filepath.Join("node_modules", ".bin", "skills")

// staleInstall is the age after which an unfinished install directory is
// removed. An install takes seconds, so an older one is from a killed process.
const staleInstall = time.Hour

// ToolError is a failure to set up the pinned `skills` CLI (Node, npm, or the
// cache), not a failure of the source.
type ToolError struct{ Err error }

func (e *ToolError) Error() string { return e.Err.Error() }
func (e *ToolError) Unwrap() error { return e.Err }

// IsToolError reports whether err comes from the setup of the `skills` CLI.
func IsToolError(err error) bool {
	var te *ToolError
	return errors.As(err, &te)
}

// SkillsCLIVersion returns the pinned version of the `skills` CLI.
func SkillsCLIVersion() string {
	var pj struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(skillmeister.SkillsPackageJSON, &pj) != nil || pj.DevDependencies["skills"] == "" {
		return "unknown"
	}
	return pj.DevDependencies["skills"]
}

// cacheBase returns the absolute directory that holds the CLI installs.
func cacheBase() (string, error) {
	base := os.Getenv(CacheEnv)
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("no cache directory for the `skills` CLI (%v); set %s", err, CacheEnv)
		}
		base = filepath.Join(dir, "skillmeister")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("%s: %w", CacheEnv, err)
	}
	return abs, nil
}

// SkillsCLIDir returns the directory of the pinned CLI install. The name holds
// the version and a hash of the lock, so a new pin uses a new directory.
func SkillsCLIDir() (string, error) {
	base, err := cacheBase()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(skillmeister.SkillsPackageLock)
	return filepath.Join(base, "skills-"+SkillsCLIVersion()+"-"+hex.EncodeToString(sum[:])[:8]), nil
}

// SkillsCLI returns the path of the pinned `skills` CLI. It installs the CLI
// into the cache when it is not there. Only an npx fetch calls it, so a
// command that only reads the library never needs Node.
func SkillsCLI(log io.Writer) (string, error) {
	bin, err := skillsCLI(log)
	if err != nil {
		return "", &ToolError{err}
	}
	return bin, nil
}

func skillsCLI(log io.Writer) (string, error) {
	if log == nil {
		log = io.Discard
	}
	if err := checkNode(); err != nil {
		return "", err
	}
	dir, err := SkillsCLIDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, skillsBinRel)
	if isFile(bin) {
		return bin, nil
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return "", errors.New("npx packs need npm to install the pinned `skills` CLI, and npm is not in PATH; " +
			"install Node.js with npm, or add the repo by its URL (https://github.com/OWNER/REPO) to fetch it with git")
	}
	base := filepath.Dir(dir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("cannot create the cache directory %s: %w; set %s to a writable directory", base, err, CacheEnv)
	}
	removeStaleInstalls(base)
	tmp, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", fmt.Errorf("cannot write to the cache directory %s: %w; set %s to a writable directory", base, err, CacheEnv)
	}
	defer os.RemoveAll(tmp)
	for name, data := range map[string][]byte{"package.json": skillmeister.SkillsPackageJSON, "package-lock.json": skillmeister.SkillsPackageLock} {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			return "", err
		}
	}
	fmt.Fprintf(log, "installing the pinned skills CLI %s in %s...\n", SkillsCLIVersion(), dir)
	// The CLI is a dev dependency, so --include=dev overrides an npmrc that
	// omits dev dependencies. No install script runs.
	if _, err := run(tmp, nil, "npm", "ci", "--ignore-scripts", "--include=dev", "--no-audit", "--no-fund", "--loglevel=error"); err != nil {
		return "", fmt.Errorf("install the pinned `skills` CLI: %w", err)
	}
	if !isFile(filepath.Join(tmp, skillsBinRel)) {
		return "", fmt.Errorf("install the pinned `skills` CLI: npm ci did not create %s", skillsBinRel)
	}
	// A directory without the CLI is a broken install. Another process can
	// finish its install first; then its directory is used.
	for attempt := 0; ; attempt++ {
		err := os.Rename(tmp, dir)
		if err == nil || isFile(bin) {
			return bin, nil
		}
		if attempt > 0 {
			return "", fmt.Errorf("install the pinned `skills` CLI: %w", err)
		}
		// A directory without the CLI is a broken install. Move it aside
		// with a rename, so that a good install that another process moves in
		// at the same time is not deleted.
		if aside, err := os.MkdirTemp(base, ".install-old-"); err == nil {
			os.Rename(dir, filepath.Join(aside, "x"))
			os.RemoveAll(aside)
		}
	}
}

// removeStaleInstalls removes the unfinished installs of killed processes.
func removeStaleInstalls(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".install-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleInstall {
			os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

var nodeVersionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// checkNode checks that node is in PATH and meets the engines field of the
// pinned CLI. The CLI starts with `#!/usr/bin/env node`, so each run needs it.
func checkNode() error {
	if _, err := exec.LookPath("node"); err != nil {
		return errors.New("npx packs need Node.js to run the pinned `skills` CLI, and node is not in PATH; " +
			"install Node.js, or add the repo by its URL (https://github.com/OWNER/REPO) to fetch it with git")
	}
	want := requiredNode()
	if want == nil {
		return nil
	}
	out, err := run("", nil, "node", "--version")
	if err != nil {
		return fmt.Errorf("node --version: %w", err)
	}
	have := parseVersion(strings.TrimSpace(out))
	if have == nil {
		return nil
	}
	for i := range want {
		if have[i] != want[i] {
			if have[i] < want[i] {
				return fmt.Errorf("the pinned `skills` CLI needs Node.js %d.%d.%d or newer, and node is %s",
					want[0], want[1], want[2], strings.TrimSpace(out))
			}
			return nil
		}
	}
	return nil
}

// requiredNode returns the minimum Node version from the lock, or nil when the
// engines field is not a simple ">=X.Y.Z".
func requiredNode() []int {
	var lk struct {
		Packages map[string]struct {
			Engines map[string]string `json:"engines"`
		} `json:"packages"`
	}
	if json.Unmarshal(skillmeister.SkillsPackageLock, &lk) != nil {
		return nil
	}
	e := strings.TrimSpace(lk.Packages["node_modules/skills"].Engines["node"])
	if !strings.HasPrefix(e, ">=") {
		return nil
	}
	return parseVersion(strings.TrimSpace(strings.TrimPrefix(e, ">=")))
}

func parseVersion(s string) []int {
	m := nodeVersionRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	v := make([]int, 3)
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v
}
