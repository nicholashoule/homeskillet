// Ownership and permissions on Windows, read from each file's security
// descriptor: its owner, and its discretionary access control list (DACL),
// which says who may do what to it.
//
// The rules mirror the Unix ones. The current user, SYSTEM, the
// Administrators group, and TrustedInstaller are trusted, as the user and root
// are on Unix: Windows itself gives the profile directory, and often other
// directories, to one of the last three, and an administrator can take any
// file anyway. A file is private when a trusted account owns it and the DACL
// lets nobody else change it; a shared ancestor may also let others add
// entries, which is what a sticky directory allows on Unix, but not remove or
// rename them. Anything the checks cannot evaluate fails them.

package trust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// Access rights, from winnt.h. Names for files and directories share bits:
// FILE_WRITE_DATA is FILE_ADD_FILE, and FILE_APPEND_DATA is
// FILE_ADD_SUBDIRECTORY.
const (
	fileWriteData       = 0x2
	fileAppendData      = 0x4
	fileWriteEA         = 0x10
	fileDeleteChild     = 0x40
	fileWriteAttributes = 0x100
	deleteAccess        = 0x10000
	readControl         = 0x20000
	writeDAC            = 0x40000
	writeOwner          = 0x80000
	genericAll          = 0x10000000
	genericWrite        = 0x40000000

	// privateRights are every right that changes a file or directory, its
	// entries, who owns it, or who may access it.
	privateRights = fileWriteData | fileAppendData | fileWriteEA | fileDeleteChild |
		fileWriteAttributes | deleteAccess | writeDAC | writeOwner | genericAll | genericWrite
	// sharedRights leave out adding a file or subdirectory. Without
	// FILE_DELETE_CHILD or DELETE, whoever adds an entry cannot remove or
	// rename anyone else's, so, like a sticky directory, an ancestor that
	// lets others add entries still cannot be swapped for another.
	sharedRights = privateRights &^ (fileWriteData | fileAppendData)
)

// Well-known security identifiers.
const (
	sidSystem           = "S-1-5-18"
	sidAdministrators   = "S-1-5-32-544"
	sidTrustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	// The creator SIDs stand for whoever creates an object, and appear in
	// access control entries meant to be inherited; no process runs as
	// either, so an entry that names one grants nobody anything. OWNER
	// RIGHTS stands for the owner, already required to be trusted.
	sidCreatorOwner = "S-1-3-0"
	sidCreatorGroup = "S-1-3-1"
	sidOwnerRights  = "S-1-3-4"
)

// currentUser returns the security identifier of the user this process runs
// as, read once from its access token.
var currentUser = sync.OnceValues(func() (string, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close() //nolint:errcheck // a token opened only for reading; closing it cannot lose anything
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String()
})

// trusted reports whether sid names the current user, SYSTEM, the
// Administrators group, or TrustedInstaller.
func trusted(sid string) bool {
	user, err := currentUser()
	return (err == nil && sid == user) || sid == sidSystem || sid == sidAdministrators || sid == sidTrustedInstaller
}

// checkShared allows path to belong to the current user or the system, and
// others only to add entries to it if it is a directory (see sharedRights).
func checkShared(path string, info os.FileInfo) error {
	rights := uint32(privateRights)
	if info.IsDir() {
		rights = sharedRights
	}
	return checkPath(path, rights)
}

// checkPrivate requires path to belong to the current user or the system and
// to be writable by nobody else.
func checkPrivate(path string, _ os.FileInfo) error {
	return checkPath(path, privateRights)
}

// checkOwner requires path, following links, to belong to the current user
// or the system.
func checkOwner(path string, _ os.FileInfo) error {
	descriptor, err := pathSecurity(path, true)
	if err != nil {
		return err
	}
	return descriptor.checkOwner(path)
}

// checkOpenProgram applies the private rule to a program homeskillet is about
// to copy, reading the security descriptor through the open file, so the
// check covers the bytes copied.
func checkOpenProgram(file *os.File, _ os.FileInfo) error {
	descriptor, err := handleSecurity(syscall.Handle(file.Fd()))
	if err != nil {
		return fmt.Errorf("%s: %w", printable(file.Name()), err)
	}
	return descriptor.check(file.Name(), privateRights)
}

// checkPath checks path itself, never what it links to, against rights: the
// rights that nobody but a trusted account may hold.
//
// A junction redirects every path through it, as a symlink does, but Go
// neither reports one as a symlink nor resolves one in filepath.EvalSymlinks,
// so a path through one would reach files that no check here saw, and
// containment, decided on path strings, would be wrong. Junctions are
// therefore refused, as is every other reparse point that is not a
// directory, such as a OneDrive placeholder, whose contents Windows fetches
// on demand from elsewhere. OneDrive directories are directories, and pass.
func checkPath(path string, rights uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 && !info.IsDir() {
		return fmt.Errorf("%s: a symlink, junction, or other reparse point, such as a OneDrive file, which homeskillet does not follow here; use the directory or file it stands for", printable(path))
	}
	descriptor, err := pathSecurity(path, false)
	if err != nil {
		return err
	}
	return descriptor.check(path, rights)
}

// descriptor is the part of a security descriptor the checks use.
type descriptor struct {
	owner string // the owner's security identifier, such as S-1-5-18
	// dacl holds the DACL's bytes, or nil for a null DACL, which grants
	// everyone every right.
	dacl []byte
}

// checkOwner requires a trusted owner, who could otherwise grant themselves
// any right.
func (d descriptor) checkOwner(path string) error {
	if !trusted(d.owner) {
		return fmt.Errorf("%s: owned by %s, not you, SYSTEM, or Administrators; use files and folders you own instead", printable(path), account(d.owner))
	}
	return nil
}

// check requires a trusted owner and a DACL that grants none of rights to
// anyone untrusted.
func (d descriptor) check(path string, rights uint32) error {
	if err := d.checkOwner(path); err != nil {
		return err
	}
	if d.dacl == nil {
		return fmt.Errorf("%s: permissions too open: it has no access control list, so everyone can change it; fix them so only you can change it", printable(path))
	}
	entries, err := parseACL(d.dacl)
	if err != nil {
		return fmt.Errorf("%s: %w", printable(path), err)
	}
	for _, entry := range entries {
		if entry.mask&rights == 0 || trusted(entry.sid) {
			continue
		}
		switch entry.sid {
		case sidCreatorOwner, sidCreatorGroup, sidOwnerRights:
			continue
		}
		return writableError(path, entry.sid, entry.mask)
	}
	return nil
}

// profile is the current user's profile directory, found once, or "" when
// Windows cannot say where it is.
var profile = sync.OnceValue(func() string {
	path, err := host.Profile.Path()
	if err != nil || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
})

// writableError reports a path that someone untrusted may change, given the
// access mask of the entry that lets them, in one line, as every refusal here
// does: the path, what is wrong, and what to do. Folders made at the root of a
// drive, such as C:\src, let every signed-in user change what is in them,
// while a profile lets only its user, so a folder outside the profile can
// move into it instead. That cannot help with a directory above the profile,
// such as C:\, which every check of a file in the profile passes through:
// only an administrator can repair it. The permission is named as a folder's
// Security tab names it, so that it can be matched with what icacls shows.
// The messages name no documentation, which can move; the Windows guide has
// the commands.
func writableError(path, sid string, mask uint32) error {
	home := profile()
	switch {
	case home != "" && Within(path, home) && !strings.EqualFold(path, home):
		return fmt.Errorf("%s: permissions too open: %s can change it (%s), and your profile is inside it, so moving files cannot help; "+
			"an administrator needs to repair them by restoring what Windows sets (icacls %s shows them)",
			printable(path), account(sid), permission(mask), printable(path))
	case home != "" && Within(home, path):
		return fmt.Errorf("%s: permissions too open: %s can change it; fix them so only you can change it", printable(path), account(sid))
	}
	return fmt.Errorf("%s: permissions too open: %s can change it; fix them so only you can change it, or move it into your profile", printable(path), account(sid))
}

// permission names mask as a folder's Security tab does when it holds one of
// the basic permissions that allow changes, or else as special permissions.
func permission(mask uint32) string {
	const (
		fullControl = 0x1f01ff // FILE_ALL_ACCESS
		modify      = 0x1301bf // Write, Read & execute, and DELETE
		write       = fileWriteData | fileAppendData | fileWriteEA | fileWriteAttributes
	)
	switch {
	case mask&genericAll != 0 || mask&fullControl == fullControl:
		return "Full control"
	case mask&modify == modify:
		return "Modify"
	case mask&genericWrite != 0 || mask&write == write:
		return "Write"
	}
	return "special permissions"
}

// account returns the name of the account sid identifies, such as
// BUILTIN\Users, or sid itself when Windows cannot name it.
func account(sid string) string {
	parsed, err := syscall.StringToSid(sid)
	if err != nil {
		return sid
	}
	name, domain, _, err := parsed.LookupAccount("")
	switch {
	case err != nil || name == "":
		return sid
	case domain == "":
		return name
	}
	return domain + `\` + name
}

// accessEntry is an access control entry that grants rights: mask to sid.
type accessEntry struct {
	sid  string
	mask uint32
}

// Access control entry types and flags, from winnt.h.
const (
	accessAllowedType              = 0x0
	accessDeniedType               = 0x1
	accessDeniedObjectType         = 0x6
	accessAllowedCallbackType      = 0x9
	accessDeniedCallbackType       = 0xA
	accessDeniedCallbackObjectType = 0xC
	inheritOnlyFlag                = 0x8
)

// parseACL returns the entries of acl, an ACL structure, that grant rights to
// the object itself. Entries that deny rights only take rights away, and
// inherit-only entries apply only to files created in a directory later,
// which are checked when they exist, so neither is returned. An entry of any
// other type is an error, since what it grants cannot be known here.
func parseACL(acl []byte) ([]accessEntry, error) {
	malformed := errors.New("malformed access control list")
	// ACL: revision, padding, size (16 bits), entry count (16 bits), padding.
	if len(acl) < 8 {
		return nil, malformed
	}
	count := int(uint16(acl[4]) | uint16(acl[5])<<8)
	var entries []accessEntry
	offset := 8
	for range count {
		// ACE_HEADER: type, flags, size (16 bits), then the body.
		if offset+4 > len(acl) {
			return nil, malformed
		}
		kind, flags := acl[offset], acl[offset+1]
		size := int(uint16(acl[offset+2]) | uint16(acl[offset+3])<<8)
		if size < 4 || offset+size > len(acl) {
			return nil, malformed
		}
		body := acl[offset+4 : offset+size]
		offset += size
		switch kind {
		case accessDeniedType, accessDeniedObjectType, accessDeniedCallbackType, accessDeniedCallbackObjectType:
			continue
		case accessAllowedType, accessAllowedCallbackType:
		default:
			return nil, fmt.Errorf("access control entry of type %d cannot be evaluated", kind)
		}
		if flags&inheritOnlyFlag != 0 {
			continue
		}
		// The body is the access mask, then the SID; a callback entry's
		// condition follows the SID and only narrows when it applies.
		if len(body) < 4 {
			return nil, malformed
		}
		sid, ok := sidString(body[4:])
		if !ok {
			return nil, malformed
		}
		entries = append(entries, accessEntry{sid: sid, mask: le32(body)})
	}
	return entries, nil
}

// sidString formats the binary security identifier at the start of b as
// ConvertSidToStringSid does, such as S-1-5-32-544.
func sidString(b []byte) (string, bool) {
	// SID: revision, subauthority count, 48-bit big-endian identifier
	// authority, then 32-bit little-endian subauthorities.
	if len(b) < 8 || b[0] != 1 || b[1] > 15 || len(b) < 8+4*int(b[1]) {
		return "", false
	}
	var authority uint64
	for _, octet := range b[2:8] {
		authority = authority<<8 | uint64(octet)
	}
	text := "S-1-" + strconv.FormatUint(authority, 10)
	if authority >= 1<<32 {
		text = fmt.Sprintf("S-1-0x%012X", authority)
	}
	for i := range int(b[1]) {
		text += "-" + strconv.FormatUint(uint64(le32(b[8+4*i:])), 10)
	}
	return text, true
}

// le32 reads a little-endian 32-bit value from the start of b.
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// getSecurityInfo is advapi32's GetSecurityInfo. The standard library
// declares no security descriptor functions, and homeskillet takes no
// dependencies, so it is declared here as package host declares its own.
var getSecurityInfo = syscall.NewLazyDLL("advapi32.dll").NewProc("GetSecurityInfo")

// pathSecurity returns the owner and DACL of path, or of what it links to
// when follow is set. Without follow, the file is opened as a reparse point,
// so a link's own descriptor is read and nothing is fetched from OneDrive.
func pathSecurity(path string, follow bool) (descriptor, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return descriptor{}, err
	}
	// Backup semantics lets CreateFile open a directory. Read control is
	// the one right needed, and every share mode leaves other programs
	// free to use the file meanwhile.
	flags := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := syscall.CreateFile(name, readControl,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		return descriptor{}, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer syscall.CloseHandle(handle) //nolint:errcheck // opened only to read; closing it cannot lose anything
	result, err := handleSecurity(handle)
	// Some file systems keep no Windows security at all, as a WSL
	// distribution's does when mapped to a drive letter. Nothing could vouch
	// for their files, so they fail, but with an error that says why.
	if errors.Is(err, errorInvalidFunction) {
		err = errors.New("no Windows owner or access list is kept for it, as for files inside WSL; keep them under your Windows profile")
	}
	if err != nil {
		return descriptor{}, &os.PathError{Op: "read security of", Path: path, Err: err}
	}
	return result, nil
}

// errorInvalidFunction is ERROR_INVALID_FUNCTION, which reading the security
// of a file on a file system without any returns.
const errorInvalidFunction syscall.Errno = 1

// handleSecurity returns the owner and DACL of the open file handle.
func handleSecurity(handle syscall.Handle) (descriptor, error) {
	const (
		fileObject              = 1 // SE_FILE_OBJECT
		ownerSecurityInfomation = 0x1
		daclSecurityInformation = 0x4
	)
	var owner *syscall.SID
	var dacl *byte
	var security uintptr
	status, _, _ := getSecurityInfo.Call(uintptr(handle), fileObject, ownerSecurityInfomation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, //nolint:gosec // G103: the API writes its results through these pointers
		uintptr(unsafe.Pointer(&dacl)), 0, //nolint:gosec // G103: as above
		uintptr(unsafe.Pointer(&security))) //nolint:gosec // G103: as above
	if status != 0 {
		return descriptor{}, syscall.Errno(status)
	}
	// The owner and DACL point into the descriptor, which Windows allocated
	// and the caller frees, so both are copied out first.
	defer syscall.LocalFree(syscall.Handle(security)) //nolint:errcheck // freeing memory cannot fail in a way worth reporting
	if owner == nil {
		return descriptor{}, errors.New("no owner")
	}
	sid, err := owner.String()
	if err != nil {
		return descriptor{}, err
	}
	result := descriptor{owner: sid}
	if dacl != nil {
		// ACL's third and fourth bytes give its size, entries included. It
		// stays allocated until the LocalFree above.
		header := unsafe.Slice(dacl, 8) //nolint:gosec // G103: the ACL Windows returned, whose header is 8 bytes
		size := int(uint16(header[2]) | uint16(header[3])<<8)
		result.dacl = append([]byte(nil), unsafe.Slice(dacl, size)...) //nolint:gosec // G103: the ACL Windows returned, as long as its header says
	}
	return result, nil
}
