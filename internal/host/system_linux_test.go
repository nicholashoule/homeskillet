//go:build linux

// Tests of describing Linux: reading os-release.

package host

import "testing"

func TestOSRelease(t *testing.T) {
	for text, want := range map[string]string{
		"NAME=\"Fedora Linux\"\nVERSION=\"44 (Workstation Edition)\"\nPRETTY_NAME=\"Fedora Linux 44 (Workstation Edition)\"\n": "Fedora Linux 44 (Workstation Edition)",
		"NAME='Alpine Linux'\nVERSION_ID=3.20\n":   "Alpine Linux",
		"NAME=Debian\nVERSION=\"12 (bookworm)\"\n": "Debian 12 (bookworm)",
		"": "",
	} {
		if got := osRelease(text); got != want {
			t.Errorf("osRelease(%q) = %q, want %q", text, got, want)
		}
	}
	if system := DescribeSystem(); system.Kernel == "" {
		t.Errorf("no kernel release: %+v", system)
	}
}
