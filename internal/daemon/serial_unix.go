// SPDX-License-Identifier: MIT

//go:build !windows

package daemon

import "runtime"

// LocalSerialPorts lists the serial devices of this machine that are worth
// offering in the desktop application.
func LocalSerialPorts() []string {
	if runtime.GOOS == "darwin" {
		return globAll([]string{"/dev/cu.*"})
	}
	return globAll([]string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyAMA*", "/dev/ttyS[0-9]", "/dev/serial/by-id/*"})
}
