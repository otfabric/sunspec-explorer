// SPDX-License-Identifier: MIT

//go:build windows

package daemon

import (
	"strconv"
	"syscall"
	"unsafe"
)

var queryDosDevice = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryDosDeviceW")

// maxCOMPort is the highest COM port number Windows assigns.
const maxCOMPort = 256

// LocalSerialPorts lists the COM ports of this machine, in numeric order.
// Windows has no directory of serial devices: a port exists when its DOS
// device name resolves.
func LocalSerialPorts() []string {
	ports := []string{}
	target := make([]uint16, 512)
	for n := 1; n <= maxCOMPort; n++ {
		name := "COM" + strconv.Itoa(n)
		ptr, err := syscall.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		ret, _, _ := queryDosDevice.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&target[0])), uintptr(len(target)))
		if ret != 0 {
			ports = append(ports, name)
		}
	}
	return ports
}
