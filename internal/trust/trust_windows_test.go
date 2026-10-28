// Tests of the Windows ownership and permission rules: parsing access control
// lists, refusing what others may change and junctions, and the system
// locations.

package trust

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// root returns a canonical temporary directory, under the user's profile,
// whose ancestors pass the rules.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectory(dir); err != nil {
		t.Skipf("the temporary directory fails the rules, so nothing here can be tested: %v", err)
	}
	return dir
}

// icacls changes path's access control list, as a user would.
func icacls(t *testing.T, path string, args ...string) {
	t.Helper()
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	if output, err := exec.CommandContext(t.Context(), icacls, append([]string{path}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // G204: the test's own fixture
		t.Fatalf("icacls %s %v: %v\n%s", path, args, err, output)
	}
}

func TestPrivateUnlessOthersMayWrite(t *testing.T) {
	dir := root(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckTree(dir); err != nil {
		t.Fatalf("private tree rejected: %v", err)
	}
	// S-1-5-32-545 is BUILTIN\Users, named by SID so the test does not
	// depend on the system's language.
	for _, test := range []struct {
		name, path, grant string
	}{
		{"file", file, "*S-1-5-32-545:(W)"},
		{"file deletable", file, "*S-1-5-32-545:(D)"},
		{"directory", dir, "*S-1-5-32-545:(AD)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			icacls(t, test.path, "/grant", test.grant)
			err := CheckTree(dir)
			icacls(t, test.path, "/remove:g", "*S-1-5-32-545")
			if err == nil || !strings.Contains(err.Error(), test.path) || !strings.Contains(err.Error(), "Users") {
				t.Fatalf("%s writable by Users accepted: %v", test.path, err)
			}
			if err := CheckTree(dir); err != nil {
				t.Fatalf("private again, still rejected: %v", err)
			}
		})
	}
}

func TestSharedAncestorMayLetOthersAddEntries(t *testing.T) {
	dir := root(t)
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	// Adding files and subdirectories is what the root of a drive lets
	// every user do, and, like a sticky directory, cannot touch the
	// repository.
	icacls(t, dir, "/grant", "*S-1-5-32-545:(WD,AD)")
	if err := CheckDirectory(repo); err != nil {
		t.Fatalf("ancestor that others may only add to rejected: %v", err)
	}
	// Deleting entries would let them swap the repository for their own.
	icacls(t, dir, "/grant", "*S-1-5-32-545:(DC)")
	if err := CheckDirectory(repo); err == nil {
		t.Fatal("ancestor whose entries others may delete accepted")
	}
}

func TestJunctionsRefused(t *testing.T) {
	dir := root(t)
	target, junction := filepath.Join(dir, "target"), filepath.Join(dir, "tree", "junction")
	for _, d := range []string{target, filepath.Dir(junction)} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", junction, target) //nolint:gosec // G204: the test's own fixture
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, output)
	}
	tree := filepath.Dir(junction)
	if err := CheckTree(tree); err == nil {
		t.Fatal("tree holding a junction accepted")
	}
	if _, err := Resolve(tree, filepath.Join(junction, "file")); err == nil {
		t.Fatal("path through a junction resolved")
	}
	if err := CheckDirectory(junction); err == nil {
		t.Fatal("junction accepted as a directory")
	}
}

func TestParseACL(t *testing.T) {
	// A SID: revision 1, n subauthorities, authority 5 (NT AUTHORITY), then
	// each subauthority little-endian.
	sid := func(subauthorities ...uint32) []byte {
		b := []byte{1, byte(len(subauthorities)), 0, 0, 0, 0, 0, 5} //nolint:gosec // G115: a fixture of a few bytes
		for _, s := range subauthorities {
			b = append(b, byte(s), byte(s>>8), byte(s>>16), byte(s>>24)) //nolint:gosec // G115: splitting a value into its bytes
		}
		return b
	}
	entry := func(kind, flags byte, mask uint32, sid []byte) []byte {
		size := 8 + len(sid)
		b := []byte{kind, flags, byte(size), byte(size >> 8), byte(mask), byte(mask >> 8), byte(mask >> 16), byte(mask >> 24)} //nolint:gosec // G115: splitting values into their bytes
		return append(b, sid...)
	}
	acl := func(entries ...[]byte) []byte {
		var body []byte
		for _, e := range entries {
			body = append(body, e...)
		}
		size := 8 + len(body)
		return append([]byte{2, 0, byte(size), byte(size >> 8), byte(len(entries)), 0, 0, 0}, body...) //nolint:gosec // G115: a fixture of a few bytes
	}
	users := sid(32, 545)
	parsed, err := parseACL(acl(
		entry(accessAllowedType, 0, 0x1f01ff, sid(18)),
		entry(accessDeniedType, 0, fileWriteData, users),
		entry(accessAllowedType, inheritOnlyFlag, genericAll, users),
		entry(accessAllowedType, 0, fileWriteData, users),
	))
	if err != nil {
		t.Fatal(err)
	}
	// The denying and inherit-only entries grant the object nothing.
	want := []accessEntry{{"S-1-5-18", 0x1f01ff}, {"S-1-5-32-545", fileWriteData}}
	if len(parsed) != len(want) || parsed[0] != want[0] || parsed[1] != want[1] {
		t.Fatalf("parseACL = %v, want %v", parsed, want)
	}
	for name, bad := range map[string][]byte{
		"short":          {2, 0},
		"entry overruns": acl(entry(accessAllowedType, 0, 0, users))[:12],
		"unknown type":   acl(entry(0x5, 0, 0, users)),
		"bad SID":        acl(entry(accessAllowedType, 0, 0, []byte{1, 9, 0})),
	} {
		if _, err := parseACL(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if s, ok := sidString([]byte{1, 0, 0x10, 0, 0, 0, 0, 0}); !ok || s != "S-1-0x100000000000" {
		t.Errorf("large authority formatted as %q", s)
	}
}

func TestCheckLocationWindows(t *testing.T) {
	system := os.Getenv("SystemRoot")
	drive := filepath.VolumeName(system)
	refused := []string{
		drive + `\`, `D:\`, `\\server\share`, `\\server\share\`, drive + `\Users`, drive + `\users`,
		system, filepath.Join(system, "System32"), filepath.Join(system, "Temp"),
		drive + `\Program Files`, drive + `\program files\x`, drive + `\Program Files (x86)`, drive + `\ProgramData\x`,
		drive + `\Users\Public`, drive + `\Users\Default\AppData`, drive + `\$Recycle.Bin`, `D:\System Volume Information\x`,
		`\\?\C:\Users\x`, `\\.\PhysicalDrive0`,
		`\\wsl.localhost\Ubuntu`, `\\wsl.localhost\Ubuntu\home\you\dotfiles`, `\\WSL$\Ubuntu\home\you`,
		"", "relative", `C:relative`, `C:\Users\x\..\..\Windows`, `C:\Users\x\`, `C:/Users/x`,
	}
	for _, path := range refused {
		if err := CheckLocation(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	allowed := []string{
		drive + `\Users\someone`, drive + `\Users\Publications`, drive + `\src\dotfiles`, `D:\dotfiles`,
		drive + `\Program Filesystem`, `\\server\share\home`,
	}
	for _, path := range allowed {
		if err := CheckLocation(path); err != nil {
			t.Errorf("refused %q: %v", path, err)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, temporary} {
		if err := CheckLocation(path); err != nil {
			t.Errorf("refused %s: %v", path, err)
		}
	}
}
