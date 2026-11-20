#!/bin/sh
# Install skillmeister from this clone. Safe to run again. It needs no network.
#
#   scripts/install.sh
#
# Environment:
#   HOME          where the shims go
#   BIN_DIR       where the binary goes (default: $HOME/.local/bin)
#
# Steps: check Go, refuse to clobber, build from vendor/, install the binary,
# run `skillmeister init` (the shim), run doctor.
set -eu

die() { printf 'install: %s\n' "$*" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd -P)
[ -f "$here/skillmeister.yaml" ] || die "$here has no skillmeister.yaml; run this script from a skillmeister clone"
[ -n "${HOME:-}" ] || die "HOME is not set"
bin_dir=${BIN_DIR:-$HOME/.local/bin}
bin="$bin_dir/skillmeister"

# 1. Go 1.27 or newer. GOTOOLCHAIN=local stops any toolchain download.
command -v go >/dev/null 2>&1 || die "go is not installed (need Go 1.27 or newer)"
export GOTOOLCHAIN=local
gover=$(go env GOVERSION)
case "$gover" in
  go1.[0-9]|go1.[0-9][!0-9]*|go1.1[0-9]|go1.1[0-9][!0-9]*|go1.2[0-6]|go1.2[0-6][!0-9]*)
    die "Go 1.27 or newer is required (found $gover)" ;;
esac

# 2. Refuse early: never replace a directory that skillmeister did not create.
#    This runs before anything is written.
for dir in "$HOME/.agents/skills/skillmeister" "$HOME/.claude/skills/skillmeister"; do
  if [ -L "$dir" ]; then
    die "$dir is a symlink; remove it first"
  fi
  if [ -e "$dir" ]; then
    [ -d "$dir" ] &&
      grep -q '^name: skillmeister$' "$dir/SKILL.md" 2>/dev/null &&
      grep -qE '^"[^"]+" --library "[^"]+" find --json' "$dir/SKILL.md" 2>/dev/null ||
      die "$dir exists and is not a skillmeister shim; move it away first"
  fi
done

# 3. Build from the vendored tree.
mkdir -p "$bin_dir"
bin_dir=$(cd "$bin_dir" && pwd -P)
bin="$bin_dir/skillmeister"
tmp=$(mktemp "$bin_dir/.skillmeister.XXXXXX")
trap 'rm -f "$tmp"' EXIT
ver=$(git -C "$here" describe --tags --always --dirty 2>/dev/null || echo dev)
(cd "$here" && GOFLAGS=-mod=vendor go build -trimpath \
  -ldflags "-X github.com/shantanugoel/skillmeister/internal/version.Version=$ver" \
  -o "$tmp" ./cmd/skillmeister) || die "build failed"
chmod 755 "$tmp"
mv -f "$tmp" "$bin"
trap - EXIT
printf 'installed %s (%s)\n' "$bin" "$ver"

# 4. Render the shim into both discovery paths.
#    `init` does this. It refuses again to clobber, and it leaves this
#    library's own files as they are.
"$bin" --library "$here" init --bin "$bin"

# 5. Check the result.
"$bin" --library "$here" doctor
