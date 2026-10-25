package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
		for _, entry := range manifest.FilesFor(goos) {
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
		{"name": "fonts", "run": ["sh", "-c", "echo fonts"], "creates": ".local/share/fonts/done"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string][]string{"linux": {"oh-my-posh", "fonts"}, "darwin": {"fonts"}} {
		var got []string
		for _, step := range manifest.SetupFor(goos) {
			got = append(got, step.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}
}

func TestLoadRejectsUnsafeSetup(t *testing.T) {
	bidi := string(rune(0x202e))
	cases := map[string]Step{
		"no name":         {Run: []string{"true"}},
		"bad name":        {Name: "Oh My Posh", Run: []string{"true"}},
		"no command":      {Name: "x"},
		"empty command":   {Name: "x", Run: []string{""}},
		"escape in run":   {Name: "x", Run: []string{"echo", "safe\x1b[1A\x1b[2Kevil"}},
		"bidi in run":     {Name: "x", Run: []string{"echo", "a" + bidi + "b"}},
		"creates escape":  {Name: "x", Run: []string{"true"}, Creates: "../outside"},
		"creates root":    {Name: "x", Run: []string{"true"}, Creates: "."},
		"provides a path": {Name: "x", Run: []string{"true"}, Provides: "/usr/bin/tool"},
		"bad platform":    {Name: "x", Run: []string{"true"}, Platforms: []string{"windows"}},
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

func TestLoadStaysInsideRepository(t *testing.T) {
	outside := t.TempDir()
	touch(t, outside, FileName, "home/.bashrc")
	if err := os.WriteFile(filepath.Join(outside, FileName), []byte(`{"files": [{"source": "a", "target": "a"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, link := range map[string]string{FileName: FileName, HomeDir: HomeDir} {
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
