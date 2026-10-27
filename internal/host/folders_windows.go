// Windows known folders: where Windows keeps the user's profile, Documents,
// and system directories on this machine, which may not be where their
// names suggest. OneDrive moves Documents into itself when it backs it up,
// and an administrator can move any of them.

package host

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Folder is a Windows known folder, identified by its KNOWNFOLDERID.
type Folder struct {
	name string
	id   syscall.GUID
}

// The known folders homeskillet uses, with their IDs from KnownFolders.h.
var (
	// Profile is the user's profile directory, %USERPROFILE%.
	Profile = Folder{"Profile", syscall.GUID{Data1: 0x5E6C858F, Data2: 0x0E22, Data3: 0x4760, Data4: [8]byte{0x9A, 0xFE, 0xEA, 0x33, 0x17, 0xB6, 0x71, 0x73}}}
	// Documents is the user's Documents folder, which holds PowerShell's
	// profiles.
	Documents = Folder{"Documents", syscall.GUID{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}}
	// UserProfiles holds every user's profile directory, usually C:\Users.
	UserProfiles = Folder{"UserProfiles", syscall.GUID{Data1: 0x0762D272, Data2: 0xC50A, Data3: 0x4BB0, Data4: [8]byte{0xA3, 0x82, 0x69, 0x7D, 0xCD, 0x72, 0x9B, 0x80}}}
	// Public is the profile every user shares, usually C:\Users\Public.
	Public = Folder{"Public", syscall.GUID{Data1: 0xDFDF76A2, Data2: 0xC82A, Data3: 0x4D63, Data4: [8]byte{0x90, 0x6A, 0x56, 0x44, 0xAC, 0x45, 0x73, 0x85}}}
	// Windows is the Windows directory, %SystemRoot%.
	Windows = Folder{"Windows", syscall.GUID{Data1: 0xF38BF404, Data2: 0x1D43, Data3: 0x42F2, Data4: [8]byte{0x93, 0x05, 0x67, 0xDE, 0x0B, 0x28, 0xFC, 0x23}}}
	// ProgramFiles is the directory for programs of the system's own
	// architecture.
	ProgramFiles = Folder{"ProgramFiles", syscall.GUID{Data1: 0x905E63B6, Data2: 0xC1BF, Data3: 0x494E, Data4: [8]byte{0xB2, 0x9C, 0x65, 0xB7, 0x32, 0xD3, 0xD2, 0x1A}}}
	// ProgramFilesX86 is the directory for 32-bit x86 programs.
	ProgramFilesX86 = Folder{"ProgramFilesX86", syscall.GUID{Data1: 0x7C5A40EF, Data2: 0xA0FB, Data3: 0x4BFC, Data4: [8]byte{0x87, 0x4A, 0xC0, 0xF2, 0xE0, 0xB9, 0xFA, 0x8E}}}
	// ProgramData holds data that programs share across users.
	ProgramData = Folder{"ProgramData", syscall.GUID{Data1: 0x62AB5D82, Data2: 0xFDC1, Data3: 0x4DC3, Data4: [8]byte{0xA9, 0xDD, 0x07, 0x0D, 0x1D, 0x49, 0x5D, 0x97}}}
)

var (
	shGetKnownFolderPath = syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath")
	coTaskMemFree        = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")
)

// Path returns where folder is for the user this process runs as. It does
// not require the folder to exist, so a Documents folder not created yet
// still has a path.
func (f Folder) Path() (string, error) {
	const dontVerify = 0x4000 // KF_FLAG_DONT_VERIFY
	if err := shGetKnownFolderPath.Find(); err != nil {
		return "", err
	}
	var path *uint16
	status, _, _ := shGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(&f.id)), //nolint:gosec // G103: the API reads the folder's ID through this pointer
		dontVerify, 0,
		uintptr(unsafe.Pointer(&path))) //nolint:gosec // G103: the API writes its result through this pointer
	// Windows allocates the path, even on failure, and the caller frees it.
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(path))) //nolint:errcheck,gosec // G103: freeing what the API allocated; CoTaskMemFree returns nothing
	if status != 0 || path == nil {
		return "", fmt.Errorf("finding the %s folder: HRESULT %#x", f.name, status)
	}
	return utf16String(path), nil
}

// String returns the folder's name, such as Documents.
func (f Folder) String() string {
	return f.name
}

// utf16String returns the NUL-terminated UTF-16 string at p as UTF-8.
func utf16String(p *uint16) string {
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(p), 2*n)) != 0 { //nolint:gosec // G103: walking the string Windows returned, up to its terminator
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n)) //nolint:gosec // G103: the string Windows returned, up to its terminator
}
