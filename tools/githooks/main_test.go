// Tests of the pre-commit hook installer: installing and updating its own
// hook, and never replacing one it did not install.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

// TestInstallRestoresMode checks that a hook with the right contents but
// without the executable bit, as a copy made by hand may be, is rewritten
// with mode 0700: Git ignores a hook it cannot execute.
func TestInstallRestoresMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	hook := []byte("#!/bin/sh\n" + marker + "\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-commit")
	if err := os.WriteFile(path, hook, 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := install(hook, dir)
	if err != nil || status != "updated" {
		t.Fatalf("status %q, %v", status, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode not restored: %v, %v", info, err)
	}
	if status, err := install(hook, dir); err != nil || status != "unchanged" {
		t.Fatalf("second install: %q, %v", status, err)
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
				// Windows lets only privileged accounts or Developer Mode
				// create symlinks.
				if runtime.GOOS == "windows" {
					t.Skipf("cannot create symlinks here: %v", err)
				}
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
