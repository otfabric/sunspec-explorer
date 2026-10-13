// SPDX-License-Identifier: MIT

// Package daemon holds what sunspec-explorerd needs around the engine and
// the HTTP API: its data directory, the bearer token and its rotation, the
// state file the url command reads, the listen-address rules and the target
// allow-list. It must not import Wails.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DataDirEnv names the environment variable that replaces the default data
// directory. The container image sets it, so that "sunspec-explorerd url"
// run in the container finds the daemon without a flag.
const DataDirEnv = "SUNSPEC_EXPLORER_DATA_DIR"

// DefaultDataDir returns the default data directory: the directory named by
// DataDirEnv or, normally, a per-user folder in the system's temporary
// directory. That folder is ephemeral by design: the operating system may
// clear it at any time (Debian 13 keeps /tmp in RAM).
func DefaultDataDir() string {
	if dir := os.Getenv(DataDirEnv); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "sunspec-explorer-"+userID())
}

// IsTemporary reports whether dir lies in the system's temporary directory,
// where the operating system may delete it at any time.
func IsTemporary(dir string) bool {
	tmp := filepath.Clean(os.TempDir())
	dir = filepath.Clean(dir)
	return dir == tmp || strings.HasPrefix(dir, tmp+string(filepath.Separator)) ||
		strings.HasPrefix(dir, "/tmp/") || strings.HasPrefix(dir, "/var/tmp/")
}

// EnsureDataDir creates dir with mode 0700 when it does not exist and
// refuses a directory that somebody else could have prepared: a symbolic
// link, a directory of another user, or one that others can enter. A shared
// temporary directory makes all three possible.
func EnsureDataDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("data directory %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("data directory %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("data directory %s is a symbolic link: refusing to use it", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("data directory %s is not a directory", dir)
	}
	return checkOwnerAndMode(dir, info)
}
