// Package skillmeister holds files of the repo root that the binary embeds.
package skillmeister

import _ "embed"

// SkillsPackageJSON and SkillsPackageLock pin the `skills` CLI that npx packs
// run. The binary installs this pin into a per-user cache. The library holds
// no Node files.
var (
	//go:embed package.json
	SkillsPackageJSON []byte
	//go:embed package-lock.json
	SkillsPackageLock []byte
)
