// Tests of the rules wsl applies on every system: which build a
// distribution's hardware takes, and which names may name a distribution.

package wsl

import "testing"

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
