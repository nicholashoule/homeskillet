// Tests of the pre-commit hook installer: installing and updating its own
// hook, and never replacing one it did not install.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

var hook = []byte("#!/bin/sh\n" + marker + "\nexit 0\n")

func TestInstall(t *testing.T) {
	// The hooks directory may not exist yet, as after git clone --template=.
	dir := filepath.Join(t.TempDir(), "hooks")
	for _, want := range []string{"installed", "unchanged"} {
		if status, err := install(hook, dir); err != nil || status != want {
			t.Fatalf("got %q, %v; want %q", status, err, want)
		}
	}
	newer := append(bytes.Clone(hook), "echo newer\n"...)
	if status, err := install(newer, dir); err != nil || status != "updated" {
		t.Fatalf("got %q, %v; want updated", status, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "pre-commit"))
	if err != nil || !bytes.Equal(got, newer) {
		t.Errorf("installed %q, %v; want %q", got, err, newer)
	}
}

func TestInstallKeepsOtherHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-commit")
	own := []byte("#!/bin/sh\nexec my-checks\n")
	if err := os.WriteFile(path, own, 0o700); err != nil {
		t.Fatal(err)
	}
	if status, err := install(hook, dir); err == nil {
		t.Fatalf("replaced a hook it did not install: %q", status)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, own) {
		t.Errorf("hook changed to %q, %v", got, err)
	}
}

// TestInstallRefusesSymlinks checks that a pre-commit symlink, dangling or
// not, is never replaced: the installer writes only regular files, so a
// symlink is someone else's.
func TestInstallRefusesSymlinks(t *testing.T) {
	hook := []byte("#!/bin/sh\n" + marker + "\n")
	for name, target := range map[string]string{"dangling": "missing-hook", "to this hook": "real-hook"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "real-hook"), hook, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "pre-commit")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := install(hook, dir); err == nil {
				t.Error("installed over a symlink")
			}
			if got, err := os.Readlink(path); err != nil || got != target {
				t.Fatalf("symlink replaced: %q, %v", got, err)
			}
		})
	}
}

func TestInstallNeedsMarker(t *testing.T) {
	dir := t.TempDir()
	if _, err := install([]byte("#!/bin/sh\n"), dir); err == nil {
		t.Fatal("installed a hook it could never update")
	}
	if _, err := os.Stat(filepath.Join(dir, "pre-commit")); !os.IsNotExist(err) {
		t.Errorf("wrote a hook anyway: %v", err)
	}
}

// The hook make hooks installs must carry the marker, or the next make hooks
// would refuse to replace it.
func TestHookHasMarker(t *testing.T) {
	data, err := os.ReadFile("pre-commit")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("\n"+marker+"\n")) {
		t.Errorf("pre-commit lacks %q", marker)
	}
}
