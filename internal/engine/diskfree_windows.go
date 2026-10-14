// SPDX-License-Identifier: MIT

//go:build windows

package engine

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeBytes returns the space the user can still write under dir. ok is
// false when the system does not say.
func freeBytes(dir string) (free int64, ok bool) {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var available uint64
	ret, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ret == 0 {
		return 0, false
	}
	return int64(available), true
}
