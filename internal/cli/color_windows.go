// Color codes in a Windows console, which takes them only once asked to.

package cli

import (
	"os"
	"syscall"
)

// enableVirtualTerminal is the console mode that makes a console interpret
// color codes rather than print them, which Windows 10 1511 and later take.
const enableVirtualTerminal = 0x0004

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// enableColor turns on color codes in the console file and reports whether
// it takes them. A console that is already set is left as it is; one that
// refuses, as before Windows 10, gets no colors.
func enableColor(file *os.File) bool {
	handle := syscall.Handle(file.Fd())
	var mode uint32
	if syscall.GetConsoleMode(handle, &mode) != nil {
		return false
	}
	if mode&enableVirtualTerminal != 0 {
		return true
	}
	ok, _, _ := setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminal))
	return ok != 0
}
