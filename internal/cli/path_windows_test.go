// Tests of putting install's directory on the user's Path on Windows, with
// the registry replaced, so that no test changes the real Path.

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUserPath replaces the user's Path: whether home is the user's profile
// with own, whether the Path has the directory with there, and reading and
// appending failing with readErr and appendErr. It returns what was
// appended.
func fakeUserPath(t *testing.T, own, there bool, readErr, appendErr error) *[]string {
	t.Helper()
	savedOwn, savedRead, savedAppend := ownProfile, onUserPath, appendUserPath
	t.Cleanup(func() { ownProfile, onUserPath, appendUserPath = savedOwn, savedRead, savedAppend })
	var appended []string
	ownProfile = func(string) bool { return own }
	onUserPath = func(string) (bool, error) { return there, readErr }
	appendUserPath = func(dir string) (bool, error) {
		appended = append(appended, dir)
		return appendErr == nil, appendErr
	}
	return &appended
}

func TestAddToPath(t *testing.T) {
	_, home := repository(t)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	added := "note\tadded " + bin + " to your Path, for new terminals; open one" + orRun(bin) + "\n"
	failed := errors.New("access denied")
	for _, test := range []struct {
		name               string
		own, there, dryRun bool
		readErr, writeErr  error
		want               string
		appended           bool
	}{
		{name: "added", own: true, want: added, appended: true},
		{name: "a dry run appends nothing", own: true, dryRun: true, want: added},
		{name: "there already", own: true, there: true,
			want: "note\t" + bin + " is on your Path, but this terminal started before it was; open a new one" + orRun(bin) + "\n"},
		{name: "a scratch home leaves the Path alone", want: fmt.Sprintf(notOnPathLine, bin, pathAdvice(bin))},
		{name: "unreadable", own: true, readErr: failed,
			want: "note\t" + bin + " is not on PATH, and adding it to your Path failed: access denied; " + pathAdvice(bin) + "\n"},
		{name: "unwritable", own: true, writeErr: failed, appended: true,
			want: "note\t" + bin + " is not on PATH, and adding it to your Path failed: access denied; " + pathAdvice(bin) + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			appended := fakeUserPath(t, test.own, test.there, test.readErr, test.writeErr)
			var out strings.Builder
			if err := addToPath(bin, home, test.dryRun, &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != test.want {
				t.Errorf("said %q, want %q", out.String(), test.want)
			}
			if want := map[bool]int{true: 1}[test.appended]; len(*appended) != want || want == 1 && (*appended)[0] != bin {
				t.Errorf("appended %q", *appended)
			}
		})
	}
	// A dry run of a first install checks the directory it would make.
	appended := fakeUserPath(t, true, false, nil, nil)
	missing := filepath.Join(home, "prefix", "bin")
	var out strings.Builder
	if err := addToPath(missing, home, true, &out); err != nil || !strings.HasPrefix(out.String(), "note\tadded "+missing) || len(*appended) > 0 {
		t.Errorf("dry run of a new directory: %v, %q, appended %q", err, out.String(), *appended)
	}
	// Others could add programs to a directory they may write, so it never
	// goes on the Path, and neither does one that is not there.
	shared := filepath.Join(home, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	makeShared(t, shared)
	for _, dir := range []string{shared, missing} {
		out.Reset()
		if err := addToPath(dir, home, false, &out); err != nil || !strings.HasPrefix(out.String(), "note\t"+dir+" is not on PATH, and homeskillet will not add it to your Path: ") {
			t.Errorf("%s: %v, %q", dir, err, out.String())
		}
	}
	if len(*appended) > 0 {
		t.Errorf("appended %q", *appended)
	}
}

func TestPathStep(t *testing.T) {
	const dir = `C:\Users\you\.local\bin`
	saved := selfName
	t.Cleanup(func() { selfName = saved })
	selfName = func() string { return `.\homeskillet-windows-amd64.exe` }
	fakeUserPath(t, true, true, nil, nil)
	if got := pathStep(dir, `C:\Users\you`); got != dir+" is on your Path, but this terminal started before it was: open a new one, or in this one run $env:Path += ';"+dir+"'." {
		t.Errorf("on the user's Path: %q", got)
	}
	fakeUserPath(t, true, false, nil, nil)
	if got := pathStep(dir, `C:\Users\you`); got != dir+` is not on your Path; .\homeskillet-windows-amd64.exe install adds it.` {
		t.Errorf("not on it: %q", got)
	}
	fakeUserPath(t, false, false, nil, nil)
	if got := pathStep(dir, `C:\scratch`); got != dir+" is not on PATH; "+pathAdvice(dir)+"." {
		t.Errorf("scratch home: %q", got)
	}
}

func TestOrRunQuotes(t *testing.T) {
	// PowerShell's single quotes expand nothing; a quote inside is doubled.
	if got := orRun(`C:\Users\O'Brien $x\.local\bin`); got != `, or in this one run $env:Path += ';C:\Users\O''Brien $x\.local\bin'` {
		t.Errorf("orRun = %s", got)
	}
	if got := orRun("C:\\Users\\a\x1b[2Jb\\.local\\bin"); got != "" {
		t.Errorf("control characters offered to paste: %q", got)
	}
}
