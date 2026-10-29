// Tests of the rules wsl applies on every system: which build a
// distribution's hardware takes, which names may name a distribution, and
// the environment wsl.exe runs with.

package wsl

import (
	"slices"
	"testing"
)

func TestArch(t *testing.T) {
	for machine, want := range map[string]string{"x86_64\n": "amd64", "aarch64": "arm64", "arm64": "arm64"} {
		if got, err := Arch(machine); err != nil || got != want {
			t.Errorf("Arch(%q) = %q, %v; want %q", machine, got, err, want)
		}
	}
	for _, machine := range []string{"riscv64", "i686", ""} {
		if _, err := Arch(machine); err == nil {
			t.Errorf("Arch(%q) accepted", machine)
		}
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"Ubuntu": true, "Ubuntu-24.04": true, "docker-desktop": true, "": false, "-d": false, "bad\x1bname": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

func TestEnvironment(t *testing.T) {
	for _, test := range []struct {
		environ []string
		want    []string
	}{
		{[]string{"PATH=C:\\Windows"}, []string{"PATH=C:\\Windows", "WSL_UTF8=1", UpdateCheckVariable + "=off", "WSLENV=" + UpdateCheckVariable}},
		{[]string{"WSLENV=USERPROFILE/p:GOPATH", "HOMESKILLET_UPDATE_CHECK=on"},
			[]string{"WSL_UTF8=1", UpdateCheckVariable + "=off", "WSLENV=USERPROFILE/p:GOPATH:" + UpdateCheckVariable}},
		{[]string{"WslEnv=HOMESKILLET_UPDATE_CHECK/u", "wsl_utf8=0"}, []string{"WSL_UTF8=1", UpdateCheckVariable + "=off", "WSLENV=HOMESKILLET_UPDATE_CHECK/u"}},
	} {
		if got := environment(test.environ); !slices.Equal(got, test.want) {
			t.Errorf("environment(%q) = %q, want %q", test.environ, got, test.want)
		}
	}
}
