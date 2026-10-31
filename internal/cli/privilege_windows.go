// Recognizing an elevated process on Windows, its counterpart of root.

package cli

import (
	"syscall"
	"unsafe"
)

// rootRefusal explains why homeskillet will not run elevated without
// --allow-root.
const rootRefusal = "refusing to run as an elevated administrator; run it from a terminal that is not elevated, or pass --allow-root"

// privileged reports whether homeskillet runs elevated (see runsElevated).
// Tests replace it, since GitHub's Windows runners run them elevated.
var privileged = runsElevated

// runsElevated reports whether homeskillet runs elevated: with an
// administrator's full rights, as from a terminal started with Run as
// administrator. Windows then lets it change system files, so, as root on
// Unix, an elevated run must be deliberate. A process whose elevation cannot
// be read is treated as elevated.
func runsElevated() bool {
	const tokenElevation = 20 // TOKEN_INFORMATION_CLASS TokenElevation
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return true
	}
	defer token.Close() //nolint:errcheck // a token opened only for reading; closing it cannot lose anything
	var elevated, size uint32
	err = syscall.GetTokenInformation(token, tokenElevation,
		(*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &size) //nolint:gosec // G103: the API writes TOKEN_ELEVATION, one DWORD, into it
	return err != nil || elevated != 0
}

// pathAdvice says how to put dir on PATH: with the PowerShell lines the
// Windows guide gives, which keep the user Path as Windows stores it, where
// entries such as %USERPROFILE%\AppData\Local\Microsoft\WindowsApps stay
// variables, add dir once, and tell Windows the environment changed, so
// that new terminals see it. Too long to print as a note, they are linked.
// Windows keeps each user's Path in the registry, which new terminals read;
// a profile that changes $env:Path changes it only for PowerShell.
func pathAdvice(string) string {
	return "add it to your Path as " + windowsGuide + "#putting-the-command-on-your-path shows, then open a new terminal"
}

// windowsGuide is the Windows guide, which pathAdvice points to.
const windowsGuide = project + "/blob/main/docs/windows.md"

// translator names what runs an x64 build on Arm64 hardware.
func translator() string {
	return "x64 emulation"
}
