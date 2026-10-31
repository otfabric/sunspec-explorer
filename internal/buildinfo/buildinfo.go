// SPDX-License-Identifier: MIT

// Package buildinfo holds the version of the application and of the
// libraries it was built with.
package buildinfo

import "runtime/debug"

// Version is the application version. Release builds set it with
// -ldflags "-X github.com/otfabric/sunspec-explorer/internal/buildinfo.Version=...".
var Version = "dev"

const goSunspecPath = "github.com/otfabric/go-sunspec"

// GoSunspecVersion returns the version of go-sunspec in this binary, or
// "unknown" when the binary carries no module information.
func GoSunspecVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return moduleVersion(info, goSunspecPath)
}

// moduleVersion returns the version of the dependency at path, or "unknown".
func moduleVersion(info *debug.BuildInfo, path string) string {
	for _, dep := range info.Deps {
		if dep.Path == path {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return "unknown"
}
