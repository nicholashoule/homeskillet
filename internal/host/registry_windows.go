// Reading the Windows registry: string values and the names of subkeys, as
// setup steps' PATH and WSL's distributions need.

package host

import (
	"errors"
	"syscall"
	"unsafe"
)

// maxRegistryValue bounds a value RegistryString reads. The values read are
// paths and names; a larger one is not what was asked for.
const maxRegistryValue = 1 << 20

// errorNoMoreItems is ERROR_NO_MORE_ITEMS, which ends an enumeration.
const errorNoMoreItems syscall.Errno = 259

// RegistryString returns the string value called name in the key root\key,
// as the registry holds it, so a REG_EXPAND_SZ value keeps its %VARIABLES%.
func RegistryString(root syscall.Handle, key, name string) (string, error) {
	handle, err := openKey(root, key, syscall.KEY_QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer syscall.RegCloseKey(handle) //nolint:errcheck // opened only to read; closing it cannot lose anything
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	var kind, size uint32
	if err := syscall.RegQueryValueEx(handle, value, nil, &kind, nil, &size); err != nil {
		return "", err
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return "", errors.New("registry value is not a string")
	}
	if size == 0 {
		return "", nil
	}
	if size > maxRegistryValue {
		return "", errors.New("registry value is too large")
	}
	buffer := make([]uint16, (size+1)/2)
	if err := syscall.RegQueryValueEx(handle, value, nil, &kind, (*byte)(unsafe.Pointer(&buffer[0])), &size); err != nil { //nolint:gosec // G103: the API writes the value into this buffer
		return "", err
	}
	return syscall.UTF16ToString(buffer), nil
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
