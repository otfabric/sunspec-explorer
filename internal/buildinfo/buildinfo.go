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
	for _, dep := range info.Deps {
		if dep.Path == goSunspecPath {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return "unknown"
}
