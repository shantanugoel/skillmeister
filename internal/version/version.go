// Package version reports the build version of the binary.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at build time with -ldflags "-X .../internal/version.Version=v0.1.0".
var Version string

// String returns the build version. It falls back to the module version and
// then the VCS revision from the build info.
func String() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		return "dev-" + strings.TrimSpace(rev[:min(len(rev), 12)]) + dirty
	}
	return "dev"
}
