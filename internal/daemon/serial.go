// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
)

// SerialAllowList decides which serial devices may be opened, by glob
// patterns such as "/dev/ttyUSB*". On a gateway the daemon opens none until
// its operator names them: a serial line may carry something else than the
// inverter.
type SerialAllowList struct {
	patterns []string
}

// NewSerialAllowList validates the patterns.
func NewSerialAllowList(patterns []string) (*SerialAllowList, error) {
	for _, p := range patterns {
		if _, err := filepath.Match(p, ""); err != nil {
			return nil, fmt.Errorf("allowed serial device %q is not a valid pattern", p)
		}
	}
	return &SerialAllowList{patterns: patterns}, nil
}

// Allow reports whether device matches one of the patterns.
func (l *SerialAllowList) Allow(device string) error {
	for _, p := range l.patterns {
		if ok, _ := filepath.Match(p, device); ok {
			return nil
		}
	}
	return fmt.Errorf("%s is not among the daemon's allowed serial devices (--allow-serial)", device)
}

// Ports lists the devices that exist and match a pattern.
func (l *SerialAllowList) Ports() []string { return globAll(l.patterns) }

// LocalSerialPorts lists the serial devices of this machine that are worth
// offering in the desktop application. On Windows, where ports are not
// files, it offers none: the name (COM3) is typed.
func LocalSerialPorts() []string {
	switch runtime.GOOS {
	case "darwin":
		return globAll([]string{"/dev/cu.*"})
	case "windows":
		return nil
	}
	return globAll([]string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyAMA*", "/dev/ttyS[0-9]", "/dev/serial/by-id/*"})
}

func globAll(patterns []string) []string {
	seen := make(map[string]bool)
	out := []string{}
	for _, p := range patterns {
		matches, _ := filepath.Glob(p)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}
