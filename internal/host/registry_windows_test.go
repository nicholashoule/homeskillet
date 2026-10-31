// Tests of reading and writing the registry, and expanding the variables its
// values hold. The user's Path is the real one, so it is only read; appending
// is tested on a key of the test's own, deleted afterward.

package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestExpandVariables(t *testing.T) {
	t.Setenv("HOMESKILLET_TEST_DIR", `C:\tools`)
	for in, want := range map[string]string{
		`%HOMESKILLET_TEST_DIR%\bin`:          `C:\tools\bin`,
		`%homeskillet_test_dir%\bin`:          `C:\tools\bin`,
		`%HOMESKILLET_TEST_UNSET%\bin`:        `%HOMESKILLET_TEST_UNSET%\bin`,
		`a%%b`:                                `a%%b`,
		`50%`:                                 `50%`,
		`%HOMESKILLET_TEST_DIR%;%x%;C:\plain`: `C:\tools;%x%;C:\plain`,
		`%HOMESKILLET_TEST_DIR%%HOMESKILLET_TEST_DIR%`: `C:\toolsC:\tools`,
	} {
		if got := ExpandVariables(in); got != want {
			t.Errorf("ExpandVariables(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnUserPath(t *testing.T) {
	// The first absolute entry of the user's real Path, read only, is on it.
	value, err := RegistryString(syscall.HKEY_CURRENT_USER, userEnvironment, "Path")
	if err != nil {
		t.Skipf("no Path for this user: %v", err)
	}
	for _, entry := range filepath.SplitList(ExpandVariables(value)) {
		if filepath.IsAbs(entry) {
			if on, err := OnUserPath(strings.ToUpper(filepath.Clean(entry))); err != nil || !on {
				t.Errorf("OnUserPath(%s) = %v, %v", entry, on, err)
			}
			break
		}
	}
	if on, err := OnUserPath(`C:\homeskillet-test-never-on-a-path`); err != nil || on {
		t.Errorf("a directory on no Path: %v, %v", on, err)
	}
}

func TestCheckPathEntry(t *testing.T) {
	for _, dir := range []string{`C:\Users\you\.local\bin`, `D:\tools\bin`, `C:\Users\O'Brien $x\.local\bin`} {
		if err := checkPathEntry(dir); err != nil {
			t.Errorf("refused %s: %v", dir, err)
		}
	}
	for _, dir := range []string{
		"", `bin`, `.local\bin`, `C:bin`, `C:\Users\you\..\them\bin`, `C:\Users\you\bin\`,
		`\\server\share\bin`, `\\?\C:\bin`, `\\.\C:\bin`,
		`C:\a;b\bin`, `C:\a"b\bin`, `C:\%USERPROFILE%\bin`, "C:\\a\x1b[2Jb\\bin", "C:\\a\u202eb\\bin",
	} {
		if err := checkPathEntry(dir); err == nil {
			t.Errorf("accepted %q", dir)
		}
	}
}

func TestAppendPath(t *testing.T) {
	// A key of the test's own, under HKEY_CURRENT_USER\Software, made and
	// deleted with Windows's reg.exe.
	key := `Software\homeskillet-test-` + strconv.Itoa(os.Getpid())
	reg := filepath.Join(os.Getenv("SystemRoot"), "System32", "reg.exe")
	if output, err := exec.CommandContext(t.Context(), reg, "add", `HKCU\`+key, "/f").CombinedOutput(); err != nil { //nolint:gosec // G204: the test's own key
		t.Skipf("reg add: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(reg, "delete", `HKCU\`+key, "/f").CombinedOutput(); err != nil { //nolint:gosec,noctx // G204: the test's own key; the test's context is done by cleanup
			t.Errorf("reg delete: %v\n%s", err, output)
		}
	})
	set := func(value string, expandable bool) {
		t.Helper()
		handle, err := openKey(syscall.HKEY_CURRENT_USER, key, syscall.KEY_SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.RegCloseKey(handle) //nolint:errcheck // the test's own key, deleted at cleanup
		if err := setString(handle, "Path", value, expandable); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (string, uint32) {
		t.Helper()
		value, kind, err := registryString(syscall.HKEY_CURRENT_USER, key, "Path")
		if err != nil {
			t.Fatal(err)
		}
		return value, kind
	}
	const dir = `C:\Users\you\.local\bin`
	// With no Path yet, dir becomes it, of the type Windows gives a Path.
	if added, err := appendPath(syscall.HKEY_CURRENT_USER, key, dir); err != nil || !added {
		t.Fatalf("no Path: %v, %v", added, err)
	}
	if value, kind := read(); value != dir || kind != syscall.REG_EXPAND_SZ {
		t.Errorf("no Path: wrote %q (type %d)", value, kind)
	}
	t.Setenv("HOMESKILLET_TEST_HOME", `C:\Users\you`)
	for _, test := range []struct {
		name, value string
		expandable  bool
		added       bool
		want        string
		kind        uint32
	}{
		{"appended, variables kept", `%USERPROFILE%\AppData\Local\Microsoft\WindowsApps`, true, true,
			`%USERPROFILE%\AppData\Local\Microsoft\WindowsApps;` + dir, syscall.REG_EXPAND_SZ},
		{"plain string kept plain", `C:\tools`, false, true, `C:\tools;` + dir, syscall.REG_SZ},
		{"a final semicolon kept", `C:\tools;`, true, true, `C:\tools;` + dir, syscall.REG_EXPAND_SZ},
		{"there already, through a variable", `C:\tools;%HOMESKILLET_TEST_HOME%\.LOCAL\bin\;C:\more`, true, false,
			`C:\tools;%HOMESKILLET_TEST_HOME%\.LOCAL\bin\;C:\more`, syscall.REG_EXPAND_SZ},
	} {
		set(test.value, test.expandable)
		added, err := appendPath(syscall.HKEY_CURRENT_USER, key, dir)
		if value, kind := read(); err != nil || added != test.added || value != test.want || kind != test.kind {
			t.Errorf("%s: added %v, %v; now %q (type %d), want %q (type %d)", test.name, added, err, value, kind, test.want, test.kind)
		}
	}
	// What cannot be one entry, and a Path that would grow too long, change
	// nothing.
	set(`C:\tools`, true)
	for _, bad := range []string{`C:\a;C:\evil`, `C:\%TEMP%`, `.local\bin`} {
		if added, err := appendPath(syscall.HKEY_CURRENT_USER, key, bad); err == nil || added {
			t.Errorf("appended %q", bad)
		}
	}
	long := `C:\` + strings.Repeat("x", maxVariable-len(`C:\`)-len(dir))
	set(long, true)
	if added, err := appendPath(syscall.HKEY_CURRENT_USER, key, dir); err == nil || added {
		t.Error("appended past the length of an environment variable")
	}
	if value, _ := read(); value != long {
		t.Error("a refused append changed the Path")
	}
}
