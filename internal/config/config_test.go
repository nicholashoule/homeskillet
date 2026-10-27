// Tests of reading a repository: manifest decoding and validation, home/
// discovery, platforms, setup steps and their run forms, and names Windows
// cannot create.

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// write creates a repository whose manifest holds data and returns its root.
func write(t *testing.T, data string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, FileName), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

// writeManifest writes data into repo as the manifest called name.
func writeManifest(t *testing.T, repo, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// touch creates each file, and its parent directories, below root.
func touch(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// symlink creates a symlink, skipping the test where the system or account
// cannot, as on Windows without Developer Mode.
func symlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

// pairs renders entries as "source>target" for compact comparison.
func pairs(entries []Entry) []string {
	var result []string
	for _, entry := range entries {
		result = append(result, filepath.ToSlash(entry.Source)+">"+filepath.ToSlash(entry.Target))
	}
	return result
}

func TestLoadRejectsMalformedManifests(t *testing.T) {
	for _, data := range []string{
		`{"files":[],"typo":true}`,
		`{"files":[{"source":"a","target":"a","platform":["linux"]}]}`,
		`{"files":[]} {}`,
		`not json`,
	} {
		t.Run(data, func(t *testing.T) {
			if _, err := Load(write(t, data)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestLoadRejectsUnsafePaths(t *testing.T) {
	cases := map[string]Entry{
		"target escape":  {Source: "config", Target: "../outside"},
		"source escape":  {Source: "../outside", Target: "config"},
		"absolute":       {Source: "config", Target: "/tmp/outside"},
		"empty":          {Source: "", Target: "config"},
		"root itself":    {Source: "config", Target: "."},
		"cleans to root": {Source: "config", Target: "a/.."},
		"bad platform":   {Source: "config", Target: "config", Platforms: []string{"macos"}},
		"escape code":    {Source: "config", Target: "config\x1b[2K"},
		"bidi override":  {Source: "config\u202e", Target: "config"},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(Manifest{Files: []Entry{{Source: "valid", Target: "valid"}, entry}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(write(t, string(data))); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}

func TestFilesFor(t *testing.T) {
	manifest, err := Load(write(t, `{"files": [
		{"source": "both", "target": "both"},
		{"source": "linux", "target": "config", "platforms": ["linux"]},
		{"source": "mac", "target": "config", "platforms": ["darwin"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][]string{"linux": {"both", "linux"}, "darwin": {"both", "mac"}} {
		var got []string
		for _, entry := range manifest.FilesFor(on(goos)) {
			got = append(got, entry.Source)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}
}

func TestSetupFor(t *testing.T) {
	manifest, err := Load(write(t, `{"setup": [
		{"name": "oh-my-posh", "run": ["bash", "setup/oh-my-posh.sh"], "platforms": ["linux"], "provides": "oh-my-posh"},
		{"name": "fonts", "run": ["sh", "-c", "echo fonts"], "creates": ".local/share/fonts/done"},
		{"name": "profile", "run": ["pwsh", "-File", "init-profile.ps1"], "platforms": ["windows"], "creates": "{documents}/PowerShell/profile.ps1"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][]string{"linux": {"oh-my-posh", "fonts"}, "darwin": {"fonts"}} {
		var got []string
		for _, step := range manifest.SetupFor(on(goos)) {
			got = append(got, step.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}
}

// TestRunForms checks that "run" holds one command or a list of them, and
// that one command is written back in the plain form.
func TestRunForms(t *testing.T) {
	manifest, err := Load(write(t, `{"setup": [
		{"name": "one", "run": ["brew", "install", "jq"]},
		{"name": "several", "run": [["brew", "update"], ["brew", "install", "jq"]]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Setup[0].Run; !slices.EqualFunc(got, Commands{{"brew", "install", "jq"}}, slices.Equal) {
		t.Errorf("one command: %q", got)
	}
	if got := manifest.Setup[1].Run; !slices.EqualFunc(got, Commands{{"brew", "update"}, {"brew", "install", "jq"}}, slices.Equal) {
		t.Errorf("several commands: %q", got)
	}
	for run, want := range map[string]Commands{
		`["true"]`:             {{"true"}},
		`[["true"],["false"]]`: {{"true"}, {"false"}},
	} {
		if data, err := json.Marshal(want); err != nil || string(data) != run {
			t.Errorf("%q marshals as %s, %v", want, data, err)
		}
	}
}

func TestLoadRejectsUnsafeSetup(t *testing.T) {
	bidi := string(rune(0x202e))
	cases := map[string]Step{
		"no name":                   {Run: Commands{{"true"}}},
		"bad name":                  {Name: "Oh My Posh", Run: Commands{{"true"}}},
		"no command":                {Name: "x"},
		"empty command":             {Name: "x", Run: Commands{{""}}},
		"empty in a list":           {Name: "x", Run: Commands{{"true"}, {}}},
		"escape in list":            {Name: "x", Run: Commands{{"true"}, {"echo", "safe\x1b[2Kevil"}}},
		"escape in run":             {Name: "x", Run: Commands{{"echo", "safe\x1b[1A\x1b[2Kevil"}}},
		"bidi in run":               {Name: "x", Run: Commands{{"echo", "a" + bidi + "b"}}},
		"creates escape":            {Name: "x", Run: Commands{{"true"}}, Creates: "../outside"},
		"creates root":              {Name: "x", Run: Commands{{"true"}}, Creates: "."},
		"creates misspelled prefix": {Name: "x", Run: Commands{{"true"}}, Creates: "{document}/x"},
		"creates documents alone":   {Name: "x", Run: Commands{{"true"}}, Creates: "{documents}/"},
		"creates documents escape":  {Name: "x", Run: Commands{{"true"}}, Creates: "{documents}/../x"},
		"provides a path":           {Name: "x", Run: Commands{{"true"}}, Provides: "/usr/bin/tool"},
		"provides a Windows path":   {Name: "x", Run: Commands{{"true"}}, Provides: `tools\tool`},
		"provides a drive":          {Name: "x", Run: Commands{{"true"}}, Provides: "C:tool"},
		"bad platform":              {Name: "x", Run: Commands{{"true"}}, Platforms: []string{"macos"}},
	}
	for name, step := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(Manifest{Setup: []Step{step}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(write(t, string(data))); err == nil {
				t.Fatal("unsafe setup step accepted")
			}
		})
	}
	for name, data := range map[string]string{
		"duplicate name": `{"setup": [{"name": "a", "run": ["true"]}, {"name": "a", "run": ["true"]}]}`,
		"unknown field":  `{"setup": [{"name": "a", "run": ["true"], "shell": true}]}`,
		"run a string":   `{"setup": [{"name": "a", "run": "true"}]}`,
		"run mixed":      `{"setup": [{"name": "a", "run": [["true"], "false"]]}]}`,
		"run nested":     `{"setup": [{"name": "a", "run": [[["true"]]]}]}`,
	} {
		if _, err := Load(write(t, data)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLoadDiscoversHome(t *testing.T) {
	want := []string{
		"home/.bashrc>.bashrc",
		"home/.config/app/config.toml>.config/app/config.toml",
		"home/.config/link>.config/link",
	}
	for name, manifest := range map[string]string{
		"no manifest":       "",
		"empty manifest":    `{}`,
		"files omitted":     `{"setup": [{"name": "a", "run": ["true"]}]}`,
		"files set to null": `{"files": null}`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if manifest != "" {
				repo = write(t, manifest)
			}
			touch(t, repo,
				"home/.bashrc",
				"home/.config/app/config.toml",
				"home/.DS_Store",
				"home/.config/.DS_Store",
				"home/Thumbs.db",
				"home/Documents/desktop.ini",
				"home/Documents/Desktop.INI",
				"home/.vim/pack/plugin/.git/config",
				"home/.vim/pack/plugin/.git/HEAD",
				"outside/ignored",
			)
			// A symlink is listed, not followed: package link decides whether
			// it resolves to an acceptable source.
			symlink(t, "app/config.toml", filepath.Join(repo, "home", ".config", "link"))
			loaded, err := Load(repo)
			if err != nil {
				t.Fatal(err)
			}
			if got := pairs(loaded.Files); !slices.Equal(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestLoadExplicitFilesDisableDiscovery(t *testing.T) {
	for data, want := range map[string][]string{
		`{"files": [], "setup": [{"name": "a", "run": ["true"]}]}`:      nil,
		`{"files": [{"source": "home/.bashrc", "target": ".profile"}]}`: {"home/.bashrc>.profile"},
	} {
		repo := write(t, data)
		touch(t, repo, "home/.bashrc", "home/.inputrc")
		loaded, err := Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		if got := pairs(loaded.Files); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", data, got, want)
		}
	}
}

func TestLoadAcceptsEmptyHome(t *testing.T) {
	// A home directory with nothing in it yet is a valid, empty configuration.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, HomeDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if loaded, err := Load(repo); err != nil || len(loaded.Files) != 0 {
		t.Fatalf("got %v, %v", loaded.Files, err)
	}
}

func TestLoadRejectsUnusableRepositories(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		// Nothing to manage most likely means --repo names the wrong directory.
		"empty directory":   func(t *testing.T) string { return t.TempDir() },
		"empty manifest":    func(t *testing.T) string { return write(t, `{}`) },
		"no files or steps": func(t *testing.T) string { return write(t, `{"files": []}`) },
		"home is a file": func(t *testing.T) string {
			repo := t.TempDir()
			touch(t, repo, HomeDir)
			return repo
		},
		// A broken manifest must fail, not silently fall back to discovery.
		"manifest is dangling": func(t *testing.T) string {
			repo := t.TempDir()
			touch(t, repo, "home/.bashrc")
			symlink(t, "missing.json", filepath.Join(repo, FileName))
			return repo
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			if loaded, err := Load(setup(t)); err == nil {
				t.Fatalf("accepted with files %v", loaded.Files)
			}
		})
	}
}

// TestNothingToManageSaysWhy checks that the error for a directory with
// nothing to manage says what is missing, so a wrong directory reads
// differently from a manifest that lists nothing.
func TestNothingToManageSaysWhy(t *testing.T) {
	for _, test := range []struct {
		repo func(t *testing.T) string
		want string
	}{
		{func(t *testing.T) string { return t.TempDir() }, "it has no bootstrap.json or dotfiles.json and no home/ directory"},
		{func(t *testing.T) string { return write(t, `{}`) }, "it has no home/ directory, and its bootstrap.json lists no setup steps"},
		{func(t *testing.T) string { return write(t, `{"files": []}`) }, "its bootstrap.json lists no files or setup steps"},
		{func(t *testing.T) string {
			repo := write(t, `{}`)
			writeManifest(t, repo, DotfilesFileName, `{"files": []}`)
			return repo
		}, "its bootstrap.json and dotfiles.json list no files or setup steps"},
	} {
		_, err := Load(test.repo(t))
		if !errors.Is(err, ErrNothingToManage) || !strings.HasSuffix(err.Error(), "is not a configuration repository: "+test.want) {
			t.Errorf("got %v, want it to end %q", err, test.want)
		}
	}
}

// TestLoadReadsEitherName checks that a manifest under its first name,
// dotfiles.json, is read as one under bootstrap.json is.
func TestLoadReadsEitherName(t *testing.T) {
	for _, name := range FileNames() {
		repo := t.TempDir()
		writeManifest(t, repo, name, `{"platforms": ["linux"], "setup": [{"name": "a", "run": ["true"]}]}`)
		loaded, err := Load(repo)
		if err != nil || len(loaded.Setup) != 1 || !slices.Equal(loaded.Platforms, []string{"linux"}) {
			t.Errorf("%s: %+v, %v", name, loaded, err)
		}
	}
}

// TestLoadMergesManifests checks that a repository with both manifests gets
// one: platforms and files combined, bootstrap.json's steps before
// dotfiles.json's, and an entry or step in both, the same in every field,
// counted once.
func TestLoadMergesManifests(t *testing.T) {
	repo := write(t, `{"platforms": ["windows"], "setup": [
		{"name": "tools", "run": ["winget", "install", "jq"]},
		{"name": "shared", "run": ["true"], "creates": "done"}
	]}`)
	writeManifest(t, repo, DotfilesFileName, `{"platforms": ["windows", "linux"], "files": [
		{"source": "home/profile.ps1", "target": ".config/powershell/profile.ps1"}
	], "setup": [
		{"name": "shared", "run": ["true"], "creates": "done"},
		{"name": "fonts", "run": ["true"]}
	]}`)
	touch(t, repo, "home/profile.ps1", "home/ignored")
	loaded, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, step := range loaded.Setup {
		steps = append(steps, step.Name)
	}
	if !slices.Equal(steps, []string{"tools", "shared", "fonts"}) {
		t.Errorf("steps %v", steps)
	}
	if !slices.Equal(loaded.Platforms, []string{"windows", "linux"}) {
		t.Errorf("platforms %v", loaded.Platforms)
	}
	// dotfiles.json lists files, so home/ is not searched.
	if got := pairs(loaded.Files); !slices.Equal(got, []string{"home/profile.ps1>.config/powershell/profile.ps1"}) {
		t.Errorf("files %v", got)
	}

	// An explicit empty list in either manifest still turns discovery off.
	repo = write(t, `{"files": [], "setup": [{"name": "a", "run": ["true"]}]}`)
	writeManifest(t, repo, DotfilesFileName, `{}`)
	touch(t, repo, "home/.bashrc")
	if loaded, err := Load(repo); err != nil || loaded.Files == nil || len(loaded.Files) != 0 {
		t.Errorf("empty files in one manifest: %v, %v", loaded.Files, err)
	}
}

// TestLoadRejectsConflictingSteps checks that two different steps with one
// name are an issue naming both manifests, rather than one silently
// replacing the other.
func TestLoadRejectsConflictingSteps(t *testing.T) {
	repo := write(t, `{"setup": [{"name": "tools", "run": ["winget", "install", "jq"]}]}`)
	writeManifest(t, repo, DotfilesFileName, `{"setup": [{"name": "tools", "run": ["winget", "install", "fzf"]}]}`)
	_, err := Load(repo)
	if err == nil || !strings.Contains(err.Error(), "bootstrap.json and dotfiles.json: setup step tools is listed twice, differently") {
		t.Fatalf("got %v", err)
	}
}

// TestLoadListsEveryIssue checks that Load reports every issue in both
// manifests, each naming its manifest, rather than stopping at the first.
func TestLoadListsEveryIssue(t *testing.T) {
	repo := write(t, `{"setup": [
		{"name": "Bad Name", "run": ["true"]},
		{"name": "ok", "run": ["true"], "creates": "{document}/x"},
		{"name": "tools", "run": ["true"]}
	]}`)
	writeManifest(t, repo, DotfilesFileName, `{"platforms": ["macos"], "files": [{"source": "../x", "target": "x"}],
		"setup": [{"name": "tools", "run": ["false"]}]}`)
	_, err := Load(repo)
	issues := Issues(err)
	want := []string{
		"bootstrap.json setup step 1: name must",
		"bootstrap.json setup step 2: creates must",
		"dotfiles.json platforms: unknown platform",
		"dotfiles.json file 1: source and target must",
		"bootstrap.json and dotfiles.json: setup step tools is listed twice",
	}
	if len(issues) != len(want) {
		t.Fatalf("got %d issues, want %d: %v", len(issues), len(want), err)
	}
	for i, issue := range issues {
		if !strings.HasPrefix(issue.Error(), want[i]) {
			t.Errorf("issue %d: %q, want it to start %q", i+1, issue, want[i])
		}
	}
	// A manifest that is not JSON is one issue, and the other's are still
	// found.
	writeManifest(t, repo, FileName, `{`)
	if issues := Issues(func() error { _, err := Load(repo); return err }()); len(issues) != 3 {
		t.Errorf("got %d issues: %v", len(issues), issues)
	}
	if Issues(nil) != nil {
		t.Error("no error, yet issues")
	}
}

// filesFor returns the source>target pairs of what loaded links on goos.
func filesFor(loaded Manifest, goos string) []string {
	return pairs(loaded.FilesFor(on(goos)))
}

// TestHomePrefixes checks that a target or creates may say it is in the home
// with ~/, $HOME/, or ${HOME}/, each read as the plain relative path, and
// that any other variable, or a source written so, is refused rather than
// taken as a directory of that name.
func TestHomePrefixes(t *testing.T) {
	loaded, err := Load(write(t, `{
		"files": [
			{"source": "a", "target": "~/.bashrc"},
			{"source": "b", "target": "$HOME/.config/b"},
			{"source": "c", "target": "${HOME}/.c"}
		],
		"setup": [
			{"name": "uv", "run": ["true"], "creates": "~/.local/bin/uv"},
			{"name": "go", "run": ["true"], "creates": "$HOME/.local/share/go/bin/go"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pairs(loaded.Files), []string{"a>.bashrc", "b>.config/b", "c>.c"}; !slices.Equal(got, want) {
		t.Errorf("targets: got %v, want %v", got, want)
	}
	if got := []string{loaded.Setup[0].Creates, loaded.Setup[1].Creates}; !slices.Equal(got, []string{".local/bin/uv", ".local/share/go/bin/go"}) {
		t.Errorf("creates: got %v", got)
	}
	for name, data := range map[string]string{
		"other user":       `{"files": [{"source": "a", "target": "~root/.bashrc"}]}`,
		"other variable":   `{"files": [{"source": "a", "target": "$XDG_CONFIG_HOME/git/config"}]}`,
		"Windows variable": `{"files": [{"source": "a", "target": "%USERPROFILE%/x"}]}`,
		"home source":      `{"files": [{"source": "~/a", "target": "a"}]}`,
		"variable source":  `{"files": [{"source": "$HOME/a", "target": "a"}]}`,
		"creates variable": `{"setup": [{"name": "x", "run": ["true"], "creates": "$XDG_DATA_HOME/x"}]}`,
		"creates ~user":    `{"setup": [{"name": "x", "run": ["true"], "creates": "~root/x"}]}`,
		"home itself":      `{"files": [{"source": "a", "target": "~"}]}`,
	} {
		if _, err := Load(write(t, data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestLoadWSLHome checks that home/wsl/ is the WSL home: its files apply to
// Linux alone, whatever the manifest's platforms say, home/'s other files to
// the other systems, and a repository without it, such as dotfiles-wsl or
// dotfiles-linux with only home/, links all of home/ on Linux.
func TestLoadWSLHome(t *testing.T) {
	repo := write(t, `{"platforms": ["windows"]}`)
	touch(t, repo, "home/.config/a", "home/wsl/.bashrc", "home/wsl/.config/b")
	loaded, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][]string{
		"windows": {"home/.config/a>.config/a"},
		"linux":   {"home/wsl/.bashrc>.bashrc", "home/wsl/.config/b>.config/b"},
		"darwin":  nil,
	} {
		if got := filesFor(loaded, goos); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}

	// Without platforms, home/ serves every system but Linux.
	repo = t.TempDir()
	touch(t, repo, "home/.profile", "home/wsl/.bashrc")
	if loaded, err = Load(repo); err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][]string{
		"windows": {"home/.profile>.profile"},
		"darwin":  {"home/.profile>.profile"},
		"linux":   {"home/wsl/.bashrc>.bashrc"},
	} {
		if got := filesFor(loaded, goos); !slices.Equal(got, want) {
			t.Errorf("no platforms, %s: got %v, want %v", goos, got, want)
		}
	}

	// Without home/wsl/, home/ is the Linux home, as in dotfiles-wsl.
	repo = write(t, `{"platforms": ["linux"]}`)
	touch(t, repo, "home/.bashrc", "home/.config/c")
	if loaded, err = Load(repo); err != nil {
		t.Fatal(err)
	}
	if got, want := filesFor(loaded, "linux"), []string{"home/.bashrc>.bashrc", "home/.config/c>.config/c"}; !slices.Equal(got, want) {
		t.Errorf("only home/: got %v, want %v", got, want)
	}
}

// TestWindowsNamesOfClonedFiles checks that a repository with anything for
// Windows, which is cloned there, holds every source to Windows' naming
// rules, even one for another system's home, and that one for Linux alone
// does not.
func TestWindowsNamesOfClonedFiles(t *testing.T) {
	files := []Entry{
		{Source: "home/wsl/con", Target: "con", Platforms: []string{"linux"}},
		{Source: "home/wsl/A", Target: "A", Platforms: []string{"linux"}},
		{Source: "home/wsl/a", Target: "a", Platforms: []string{"linux"}},
	}
	windows := Manifest{Platforms: []string{"windows"}, Files: files}
	issues := Issues(windows.checkWindowsNames())
	if len(issues) != 2 || !strings.Contains(issues[0].Error(), "reserved device name") ||
		!strings.Contains(issues[1].Error(), "sources") {
		t.Fatalf("Windows repository: %v", issues)
	}
	linux := Manifest{Platforms: []string{"linux"}, Files: files}
	if err := linux.checkWindowsNames(); err != nil {
		t.Fatalf("Linux repository: %v", err)
	}
}

func TestLoadStaysInsideRepository(t *testing.T) {
	outside := t.TempDir()
	touch(t, outside, FileName, "home/.bashrc")
	if err := os.WriteFile(filepath.Join(outside, FileName), []byte(`{"files": [{"source": "a", "target": "a"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, link := range map[string]string{FileName: FileName, DotfilesFileName: FileName, HomeDir: HomeDir} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			symlink(t, filepath.Join(outside, link), filepath.Join(repo, name))
			if loaded, err := Load(repo); err == nil {
				t.Fatalf("followed %s outside the repository: %v", name, loaded.Files)
			}
		})
	}
	// A home directory that is a symlink within the repository is followed.
	repo := t.TempDir()
	touch(t, repo, "machines/laptop/.bashrc")
	symlink(t, filepath.Join("machines", "laptop"), filepath.Join(repo, HomeDir))
	loaded, err := Load(repo)
	if got, want := pairs(loaded.Files), []string{"home/.bashrc>.bashrc"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

func TestLoadRejectsUnprintableHomeNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows forbids control characters in file names")
	}
	for _, name := range []string{"home/evil\x1b[2K", "home/dir\x1b]0;title\x07/file", "home/a" + string(rune(0x202e)) + "b"} {
		repo := t.TempDir()
		touch(t, repo, name)
		_, err := Load(repo)
		if err == nil {
			t.Fatalf("accepted %q", name)
		}
		if !Printable(err.Error()) {
			t.Fatalf("error carries the raw name: %q", err)
		}
	}
	// Names are checked before a directory is read, so a read error cannot
	// echo one either. Root reads any directory, so this needs another user.
	if os.Geteuid() == 0 {
		return
	}
	repo := t.TempDir()
	name := "locked\x1b[2K"
	locked := filepath.Join(repo, HomeDir, name)
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repo); err == nil || !Printable(err.Error()) {
		t.Fatalf("unreadable directory: %q", err)
	}
}

func TestLoadRejectsOversizedManifest(t *testing.T) {
	// Valid JSON, so only the size limit can reject it.
	padding := strings.Repeat(" ", maxManifest)
	repo := write(t, `{"setup": [{"name": "a", "run": ["true"]}]}`+padding)
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized manifest: %v", err)
	}
	repo = write(t, `{"setup": [{"name": "a", "run": ["true"]}]}`+padding[:maxManifest-100])
	if _, err := Load(repo); err != nil {
		t.Fatalf("manifest within the limit: %v", err)
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"oh-my-posh": true, "a": true, "0": true, "fonts.meslo_v2": true,
		"": false, "-a": false, ".a": false, "_a": false, "Oh": false, "a b": false,
		"a/b": false, "a\x1b": false, "\u00e9": false,
	} {
		if got := validName(name); got != want {
			t.Errorf("validName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDefaultPlatforms(t *testing.T) {
	repo := write(t, `{
		"platforms": ["darwin"],
		"files": [
			{"source": "home/default", "target": "default"},
			{"source": "home/linux", "target": "linux", "platforms": ["linux"]},
			{"source": "home/everywhere", "target": "everywhere", "platforms": ["linux", "darwin"]}
		],
		"setup": [
			{"name": "default", "run": ["true"]},
			{"name": "linux", "run": ["true"], "platforms": ["linux"]},
			{"name": "everywhere", "run": ["true"], "platforms": ["linux", "darwin"]}
		]
	}`)
	manifest, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][2][]string{
		"darwin": {{"default", "everywhere"}, {"default", "everywhere"}},
		"linux":  {{"linux", "everywhere"}, {"linux", "everywhere"}},
	} {
		var files, steps []string
		for _, entry := range manifest.FilesFor(on(goos)) {
			files = append(files, entry.Target)
		}
		for _, step := range manifest.SetupFor(on(goos)) {
			steps = append(steps, step.Name)
		}
		if !slices.Equal(files, want[0]) || !slices.Equal(steps, want[1]) {
			t.Errorf("%s: files %v, steps %v; want %v", goos, files, steps, want)
		}
	}
}

func TestDefaultPlatformsApplyToDiscoveredFiles(t *testing.T) {
	repo := write(t, `{"platforms": ["darwin"]}`)
	touch(t, repo, "home/.zshrc")
	manifest, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.FilesFor(on("linux")); len(got) != 0 {
		t.Errorf("linux got %v", pairs(got))
	}
	if got := pairs(manifest.FilesFor(on("darwin"))); !slices.Equal(got, []string{"home/.zshrc>.zshrc"}) {
		t.Errorf("darwin got %v", got)
	}
}

func TestDefaultPlatformsValidated(t *testing.T) {
	for _, data := range []string{
		`{"platforms": ["macos"], "setup": [{"name": "a", "run": ["true"]}]}`,
		`{"platforms": ["darwin/arm"], "setup": [{"name": "a", "run": ["true"]}]}`,
		`{"platforms": "darwin", "setup": [{"name": "a", "run": ["true"]}]}`,
	} {
		if _, err := Load(write(t, data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

// on returns the platform goos on arm64, as detection reports a Mac or an
// arm64 Linux machine.
func on(goos string) Platform {
	return Platform{OS: goos, Arch: "arm64"}
}

func TestParsePlatform(t *testing.T) {
	for value, want := range map[string]Platform{
		"linux":         {OS: "linux"},
		"darwin":        {OS: "darwin"},
		"darwin/arm64":  {OS: "darwin", Arch: "arm64"},
		"linux/amd64":   {OS: "linux", Arch: "amd64"},
		"windows":       {OS: "windows"},
		"windows/arm64": {OS: "windows", Arch: "arm64"},
	} {
		got, err := ParsePlatform(value)
		if err != nil || got != want || got.String() != value {
			t.Errorf("ParsePlatform(%q) = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "macos", "win", "Windows", "darwin/", "darwin/arm", "darwin/arm64/x", "Linux", "/amd64"} {
		if got, err := ParsePlatform(value); err == nil {
			t.Errorf("ParsePlatform(%q) = %v", value, got)
		}
	}
}

func TestPlatformMatches(t *testing.T) {
	mac := Platform{OS: "darwin", Arch: "arm64"}
	for value, want := range map[string]bool{
		"darwin": true, "darwin/arm64": true, "darwin/amd64": false,
		"linux": false, "linux/arm64": false, "bogus": false,
	} {
		if got := mac.Matches(value); got != want {
			t.Errorf("%v.Matches(%q) = %v", mac, value, got)
		}
	}
	// An unknown architecture matches only values that name none.
	if (Platform{OS: "darwin"}).Matches("darwin/arm64") || !(Platform{OS: "darwin"}).Matches("darwin") {
		t.Error("unknown architecture matched wrongly")
	}
}

func TestArchitecturePlatforms(t *testing.T) {
	repo := write(t, `{
		"platforms": ["darwin"],
		"setup": [
			{"name": "any-mac", "run": ["true"]},
			{"name": "apple-silicon", "run": ["true"], "platforms": ["darwin/arm64"]},
			{"name": "intel", "run": ["true"], "platforms": ["darwin/amd64"]}
		]
	}`)
	manifest, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	for arch, want := range map[string][]string{"arm64": {"any-mac", "apple-silicon"}, "amd64": {"any-mac", "intel"}} {
		var got []string
		for _, step := range manifest.SetupFor(Platform{OS: "darwin", Arch: arch}) {
			got = append(got, step.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", arch, got, want)
		}
	}
}

func TestWindowsNames(t *testing.T) {
	entry := func(target string) string {
		return `{"source": "home/x", "target": "` + target + `"}`
	}
	// On a Windows host, filepath.IsLocal refuses device names and drive
	// letters itself, before the Windows rules run, and \ separates names, so
	// the rules alone can be seen only from Linux and macOS.
	windowsHost := runtime.GOOS == "windows"
	for _, target := range []string{"CON", "nul.txt", "Documents/COM1.log", "lpt9", "COM¹", "notes.", "trailing ", "a:b", "what?", `back\\slash`, "a|b"} {
		if windowsHost && strings.Contains(target, `\`) {
			continue
		}
		data := `{"platforms": ["windows"], "files": [` + entry(target) + `]}`
		_, err := Load(write(t, data))
		if err == nil || !windowsHost && !strings.Contains(err.Error(), "Windows") {
			t.Errorf("%q accepted for Windows: %v", target, err)
		}
	}
	for _, target := range []string{"Documents/PowerShell/profile.ps1", "AppData/Roaming/app/settings.json", "console.log", "CONFIG", "nullable", ".vimrc"} {
		data := `{"platforms": ["windows"], "files": [` + entry(target) + `]}`
		if _, err := Load(write(t, data)); err != nil {
			t.Errorf("%q refused: %v", target, err)
		}
	}
	// Targets differing only in case are one file on Windows.
	data := `{"platforms": ["windows"], "files": [` + entry("Documents/a.txt") + `, ` + entry("documents/A.txt") + `]}`
	if _, err := Load(write(t, data)); err == nil || !strings.Contains(err.Error(), "ignores case") {
		t.Errorf("case collision accepted: %v", err)
	}
	// The rules apply only where Windows is a target, by default or per entry.
	// A Windows host refuses a:b anyway, as a path on drive A.
	for _, data := range []string{
		`{"files": [` + entry("a:b") + `]}`,
		`{"platforms": ["linux"], "files": [` + entry("a:b") + `]}`,
		`{"platforms": ["windows"], "files": [{"source": "home/x", "target": "a:b", "platforms": ["darwin"]}]}`,
	} {
		if _, err := Load(write(t, data)); err != nil && !windowsHost {
			t.Errorf("%s refused: %v", data, err)
		}
	}
	if _, err := Load(write(t, `{"files": [{"source": "home/x", "target": "nul", "platforms": ["windows/arm64"]}]}`)); err == nil {
		t.Error("per-entry windows platform not checked")
	}
	// Discovered files and setup records are checked too.
	repo := write(t, `{"platforms": ["windows"]}`)
	touch(t, repo, "home/AUX.json")
	if _, err := Load(repo); err == nil {
		t.Error("discovered reserved name accepted")
	}
	if _, err := Load(write(t, `{"platforms": ["windows"], "setup": [{"name": "a", "run": ["true"], "creates": "PRN"}]}`)); err == nil {
		t.Error("reserved creates accepted")
	}
}

// TestPrintableRejectsInvalidUTF8 checks that raw bytes that are not UTF-8,
// which an 8-bit terminal reads as control characters and encoding/json would
// replace, count as unprintable, as line and paragraph separators do.
func TestPrintableRejectsInvalidUTF8(t *testing.T) {
	for s, want := range map[string]bool{
		"plain": true, "caf\u00e9": true, "\x9b": false, "\xff": false, "a\x80b": false,
		"\u2028": false, "\u2029": false, "\x1b[2K": false,
	} {
		if got := Printable(s); got != want {
			t.Errorf("Printable(%q) = %v, want %v", s, got, want)
		}
	}
}
