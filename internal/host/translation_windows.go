//go:build windows

// Detecting translation on Windows: whether an x64 build runs emulated on
// Windows on Arm (see Detect).

package host

import (
	"syscall"
	"unsafe"
)

// imageFileMachineARM64 is IMAGE_FILE_MACHINE_ARM64, the machine type Windows
// reports for Arm64 hardware.
const imageFileMachineARM64 = 0xAA64

// isWow64Process2 is kernel32's IsWow64Process2, which reports the machine's
// native architecture. It exists since Windows 10 version 1709.
var isWow64Process2 = syscall.NewLazyDLL("kernel32.dll").NewProc("IsWow64Process2")

// onArm64 reports whether this process runs emulated on Windows on Arm, as an
// x64 build does there. Where IsWow64Process2 is missing or fails, the
// process is reported native.
func onArm64() bool {
	if isWow64Process2.Find() != nil {
		return false
	}
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return false
	}
	var processMachine, nativeMachine uint16
	succeeded, _, _ := isWow64Process2.Call(uintptr(process),
		uintptr(unsafe.Pointer(&processMachine)), //nolint:gosec // G103: the API writes its results through these pointers
		uintptr(unsafe.Pointer(&nativeMachine)))  //nolint:gosec // G103: as above
	return succeeded != 0 && nativeMachine == imageFileMachineARM64
}
