// Reading the Windows registry: string values and the names of subkeys, as
// setup steps' PATH and WSL's distributions need; and the one write
// homeskillet makes to it, appending a directory to the user's Path.

package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// maxRegistryValue bounds a value RegistryString reads. The values read are
// paths and names; a larger one is not what was asked for.
const maxRegistryValue = 1 << 20

// errorNoMoreItems is ERROR_NO_MORE_ITEMS, which ends an enumeration.
const errorNoMoreItems syscall.Errno = 259

// RegistryString returns the string value called name in the key root\key,
// as the registry holds it, so a REG_EXPAND_SZ value keeps its %VARIABLES%.
func RegistryString(root syscall.Handle, key, name string) (string, error) {
	value, _, err := registryString(root, key, name)
	return value, err
}

// registryString returns the string value called name in the key root\key,
// as RegistryString does, and its type: REG_SZ or REG_EXPAND_SZ.
func registryString(root syscall.Handle, key, name string) (string, uint32, error) {
	handle, err := openKey(root, key, syscall.KEY_QUERY_VALUE)
	if err != nil {
		return "", 0, err
	}
	defer syscall.RegCloseKey(handle) //nolint:errcheck // opened only to read; closing it cannot lose anything
	return queryString(handle, name)
}

// queryString returns the string value called name in the open key handle,
// and its type.
func queryString(handle syscall.Handle, name string) (string, uint32, error) {
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", 0, err
	}
	var kind, size uint32
	if err := syscall.RegQueryValueEx(handle, value, nil, &kind, nil, &size); err != nil {
		return "", 0, err
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return "", 0, errors.New("registry value is not a string")
	}
	if size == 0 {
		return "", kind, nil
	}
	if size > maxRegistryValue {
		return "", 0, errors.New("registry value is too large")
	}
	buffer := make([]uint16, (size+1)/2)
	if err := syscall.RegQueryValueEx(handle, value, nil, &kind, (*byte)(unsafe.Pointer(&buffer[0])), &size); err != nil { //nolint:gosec // G103: the API writes the value into this buffer
		return "", 0, err
	}
	return syscall.UTF16ToString(buffer), kind, nil
}

// RegistrySubkeys returns the names of the keys directly below root\key.
func RegistrySubkeys(root syscall.Handle, key string) ([]string, error) {
	handle, err := openKey(root, key, syscall.KEY_ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer syscall.RegCloseKey(handle) //nolint:errcheck // opened only to read; closing it cannot lose anything
	var names []string
	// A key name is at most 255 characters.
	const room = 256
	buffer := make([]uint16, room)
	for index := uint32(0); ; index++ {
		length := uint32(room)
		err := syscall.RegEnumKeyEx(handle, index, &buffer[0], &length, nil, nil, nil, nil)
		if errors.Is(err, errorNoMoreItems) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, syscall.UTF16ToString(buffer[:length]))
	}
}

func openKey(root syscall.Handle, key string, access uint32) (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return 0, err
	}
	var handle syscall.Handle
	err = syscall.RegOpenKeyEx(root, name, 0, access, &handle)
	return handle, err
}

// userEnvironment is the key, under HKEY_CURRENT_USER, that holds the user's
// own environment variables, Path among them, which Windows gives every
// program started after they change.
const userEnvironment = "Environment"

// maxVariable is the most UTF-16 characters, the final NUL included, an
// environment variable can hold; a longer Path would not reach the programs
// it is for.
const maxVariable = 32767

var (
	regSetValueEx      = syscall.NewLazyDLL("advapi32.dll").NewProc("RegSetValueExW")
	sendMessageTimeout = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW")
)

// OnUserPath reports whether dir is an entry of the user's Path, once the
// entry's variables, such as %USERPROFILE%, are expanded.
func OnUserPath(dir string) (bool, error) {
	value, _, err := registryString(syscall.HKEY_CURRENT_USER, userEnvironment, "Path")
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hasEntry(value, dir), nil
}

// AppendUserPath adds dir as the last entry of the user's Path, and then
// tells running programs that the environment changed, as Settings does, so
// that terminals started from then on see it. It reports false, changing
// nothing, when dir is an entry already.
//
// It is the only write homeskillet makes to the registry, and it is narrow so
// that no caller can make it write anything else: the one value, Path in
// HKEY_CURRENT_USER\Environment, named here and nowhere else; only appended
// to, so every entry already there keeps its place and its text, and a
// command of the same name earlier on the Path keeps running first; with the
// value's type kept, so that variables in it stay variables; and only with a
// dir that checkPathEntry accepts as one plain directory. The caller still
// decides whether dir belongs on the Path at all: homeskillet's own
// directory, which the ownership rules have checked.
func AppendUserPath(dir string) (bool, error) {
	added, err := appendPath(syscall.HKEY_CURRENT_USER, userEnvironment, dir)
	if added {
		announceEnvironment()
	}
	return added, err
}

// appendPath appends dir to the Path value in root\key, as AppendUserPath
// describes; tests give it a key of their own.
func appendPath(root syscall.Handle, key, dir string) (added bool, err error) {
	if err := checkPathEntry(dir); err != nil {
		return false, err
	}
	// The value is read and written through one handle, so what is
	// appended to is what was just read.
	handle, err := openKey(root, key, syscall.KEY_QUERY_VALUE|syscall.KEY_SET_VALUE)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := syscall.RegCloseKey(handle); err == nil && closeErr != nil {
			added, err = false, closeErr
		}
	}()
	value, kind, err := queryString(handle, "Path")
	switch {
	case errors.Is(err, syscall.ERROR_FILE_NOT_FOUND):
		value, kind = "", syscall.REG_EXPAND_SZ // as Windows creates it
	case err != nil:
		return false, err
	}
	if hasEntry(value, dir) {
		return false, nil
	}
	next := dir
	switch {
	case value == "":
	case strings.HasSuffix(value, ";"):
		next = value + dir
	default:
		next = value + ";" + dir
	}
	if !strings.HasPrefix(next, value) {
		return false, errors.New("the Path would not keep what it holds") // cannot happen; the write must only append
	}
	if err := setString(handle, "Path", next, kind == syscall.REG_EXPAND_SZ); err != nil {
		return false, err
	}
	return true, nil
}

// checkPathEntry accepts dir only as what one Path entry can hold and mean
// exactly: an absolute, clean path on a drive, such as C:\Users\you\.local\bin,
// not a share or a device path; with no semicolon, which would end the entry
// and start another; no double quote, which Windows reads as quoting; no
// percent sign, which Windows would expand as a variable in a REG_EXPAND_SZ
// Path, so that the entry could come to name another directory; and no
// control or formatting characters.
func checkPathEntry(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || len(filepath.VolumeName(dir)) != 2 ||
		strings.ContainsAny(dir, `;"%`) || !config.Printable(dir) {
		return fmt.Errorf("%q cannot be an entry of the Path", dir)
	}
	return nil
}

// hasEntry reports whether value, a Path as the registry holds it, has dir as
// an entry once the entries' variables are expanded, without regard to case,
// as Windows compares paths.
func hasEntry(value, dir string) bool {
	for _, entry := range filepath.SplitList(ExpandVariables(value)) {
		if filepath.IsAbs(entry) && strings.EqualFold(filepath.Clean(entry), dir) {
			return true
		}
	}
	return false
}

// setString writes value as the string value called name in the open key
// handle: REG_EXPAND_SZ when expandable, and REG_SZ otherwise. A value too
// long for an environment variable is refused.
func setString(handle syscall.Handle, name, value string, expandable bool) error {
	namePointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(value) // ends in the NUL the registry expects
	if err != nil {
		return err
	}
	if len(data) > maxVariable {
		return errors.New("the Path would be too long for Windows to use")
	}
	kind := uintptr(syscall.REG_SZ)
	if expandable {
		kind = syscall.REG_EXPAND_SZ
	}
	status, _, _ := regSetValueEx.Call(uintptr(handle), uintptr(unsafe.Pointer(namePointer)), 0, kind, //nolint:gosec // G103: the API reads the name and data from these buffers
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)) //nolint:gosec // G103: as above
	if status != 0 {
		return syscall.Errno(status)
	}
	return nil
}

// announceEnvironment broadcasts WM_SETTINGCHANGE for "Environment" to every
// top-level window, waiting at most a second on each and skipping any that
// hangs, as .NET's SetEnvironmentVariable does. Explorer then reloads the
// environment it gives the programs it starts.
func announceEnvironment() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
		waitMillis      = 1000
	)
	area, err := syscall.UTF16PtrFromString(userEnvironment)
	if err != nil {
		return
	}
	var result uintptr
	// The result says only whether every window answered in time; nothing
	// can be done about one that did not, the Path is already written, and
	// Windows reads it again at the next sign-in anyway.
	_, _, _ = sendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(area)), smtoAbortIfHung, waitMillis, //nolint:gosec // G103: the API reads the area's name from this buffer
		uintptr(unsafe.Pointer(&result))) //nolint:gosec // G103: the API writes its result here
}

// ExpandVariables replaces each %NAME% in s with the environment variable's
// value, as ExpandEnvironmentStrings does, leaving a name that is not set as
// it is.
func ExpandVariables(s string) string {
	var result strings.Builder
	for {
		start := strings.IndexByte(s, '%')
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start+1:], '%')
		if end < 0 {
			break
		}
		end += start + 1
		result.WriteString(s[:start])
		if value, ok := os.LookupEnv(s[start+1 : end]); ok && end > start+1 {
			result.WriteString(value)
		} else {
			result.WriteString(s[start : end+1])
		}
		s = s[end+1:]
	}
	result.WriteString(s)
	return result.String()
}
