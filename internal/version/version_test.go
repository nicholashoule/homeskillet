// Tests of version validity and ordering, dirty builds, and the build details
// a binary reports.

package version

import (
	"slices"
	"testing"
)

func TestValid(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.2.3":       true,
		"v0.0.0":       true,
		"v1.2.3-rc.1":  true,
		"v1.2.3+dirty": true,
		"v1.2.4-0.20261025120000-0123456789ab+dirty": true,
		"1.2.3":          false,
		"v1.2":           false,
		"v01.2.3":        false,
		"v1.2.3-01":      false,
		"v1.2.3-":        false,
		"v1.2.3-rc..1":   false,
		"v1.2.3/../../x": false,
		"v1/../2.3.4":    false,
		"v1.2.3\x1b[2K":  false,
		Devel:            false,
		"":               false,
	} {
		if got := Valid(v); got != want {
			t.Errorf("Valid(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	// Lowest precedence first, following the Semantic Versioning 2.0.0
	// examples, with Go pseudo-versions between releases.
	ordered := []string{
		"v0.0.0-20261025120000-0123456789ab",
		"v1.0.0-alpha",
		"v1.0.0-alpha.1",
		"v1.0.0-alpha.beta",
		"v1.0.0-beta",
		"v1.0.0-beta.2",
		"v1.0.0-beta.11",
		"v1.0.0-rc.1",
		"v1.0.0",
		"v1.0.1-0.20261025120000-0123456789ab",
		"v1.0.1",
		"v1.2.0",
		"v1.10.0",
		"v2.0.0",
		"v99999999999999999999.0.0",
	}
	for i, a := range ordered {
		for j, b := range ordered {
			if got, want := Compare(a, b), cmpInt(i, j); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
	if Compare("v1.0.0", "v1.0.0+dirty") != 0 {
		t.Error("build metadata affected precedence")
	}
	if Compare("invalid", "v0.0.0") != -1 || Compare("v0.0.0", "invalid") != 1 {
		t.Error("invalid versions must sort first")
	}
	shuffled := slices.Clone(ordered)
	slices.Reverse(shuffled)
	slices.SortFunc(shuffled, Compare)
	if !slices.Equal(shuffled, ordered) {
		t.Errorf("sorted to %v", shuffled)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func TestDirty(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.2.3+dirty":       true,
		"v1.2.3-pre+a.dirty": true,
		"v1.2.3":             false,
		"v1.2.3-dirty":       false,
		"v1.2.3+dirtyish":    false,
	} {
		if got := Dirty(v); got != want {
			t.Errorf("Dirty(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCurrent(t *testing.T) {
	current := Current()
	if current.Version == "" || current.Go == "" || current.Platform == "" {
		t.Fatalf("missing build details: %+v", current)
	}
}

func TestStamped(t *testing.T) {
	defer func(previous string) { stamped = previous }(stamped)
	stamped = "v9.9.9"
	if got := Current().Version; got != "v9.9.9" {
		t.Fatalf("stamped version ignored: %s", got)
	}
}

func TestDescriptions(t *testing.T) {
	info := Info{
		Version:  "v1.2.3",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		Built:    "2026-10-25T12:00:00Z",
		Modified: true,
		Go:       "go1.24.2",
		Platform: "linux/amd64",
	}
	if got, want := info.Summary(), "commit 0123456789ab (modified), built 2026-10-25T12:00:00Z, go1.24.2, linux/amd64"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	want := "  commit:    0123456789ab (modified)\n  built:     2026-10-25T12:00:00Z\n  go:        go1.24.2\n"
	if got := info.Details(); got != want {
		t.Errorf("Details = %q, want %q", got, want)
	}
	if got := (Info{Go: "go1.24.2"}).Summary(); got != "go1.24.2" {
		t.Errorf("unknown details not omitted: %q", got)
	}
}

func TestBuilt(t *testing.T) {
	defer func(saved string) { built = saved }(built)
	for value, want := range map[string]string{
		"2026-10-25T12:00:00Z":        "2026-10-25T12:00:00Z",
		"":                            "",
		"yesterday":                   "",
		"2026-10-25T12:00:00Z\x1b[2K": "",
	} {
		built = value
		if got := Current().Built; got != want {
			t.Errorf("built %q: Built = %q, want %q", value, got, want)
		}
	}
}

func TestSameSource(t *testing.T) {
	a := Info{Version: "v1.2.3", Revision: "0123456789ab", Go: "go1.24.2", Platform: "linux/amd64", Built: "2026-10-25T12:00:00Z"}
	b := a
	b.Built = "2026-10-26T12:00:00Z"
	if !a.SameSource(b) {
		t.Error("a rebuild of the same commit is not the same source")
	}
	for name, change := range map[string]func(*Info){
		"version":   func(i *Info) { i.Version = "v1.2.4" },
		"commit":    func(i *Info) { i.Revision = "ba9876543210" },
		"modified":  func(i *Info) { i.Modified = true },
		"toolchain": func(i *Info) { i.Go = "go1.25.0" },
		"platform":  func(i *Info) { i.Platform = "darwin/arm64" },
		"no commit": func(i *Info) { i.Revision = "" },
	} {
		c := a
		change(&c)
		if c.SameSource(a) || a.SameSource(c) {
			t.Errorf("different %s counted as the same source", name)
		}
	}
}
