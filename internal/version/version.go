// Package version reports which build of homeskillet is running and orders
// version strings so installations can be compared.
//
// A binary's version comes from, in order of preference:
//
//   - A value stamped at link time with
//     -ldflags "-X github.com/nicholashoule/homeskillet/internal/version.stamped=v0.0.1",
//     which `make build VERSION=v0.0.1` and `make install VERSION=v0.0.1`
//     pass. Use it where no Git metadata exists, such as a container build
//     that excludes .git.
//   - The module version Go records in every binary. Since Go 1.24, building
//     in a Git checkout records the tag (v0.0.1), or a pseudo-version for a
//     later untagged commit (v0.0.2-0.20261025120000-0123456789ab), with
//     +dirty when the tree had uncommitted changes; `go install
//     module@v0.0.1` records v0.0.1.
//
// Otherwise the version is Devel, which cannot be installed because it names
// no particular build.
package version

import (
	"cmp"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// stamped overrides the recorded module version when set at link time.
var stamped string

// built is the build time, in RFC 3339 format, when set at link time with
// -ldflags "-X github.com/nicholashoule/homeskillet/internal/version.built=...",
// as the Makefile does. Go records no build time of its own.
var built string

// Devel is the version of a build that records none.
const Devel = "(devel)"

// Info describes one homeskillet build. Package install stores it beside
// each installed binary, hence the JSON names.
type Info struct {
	// Version is a semantic version with a leading "v", or Devel.
	Version string `json:"version"`
	// Revision is the commit the binary was built from, if recorded.
	Revision string `json:"revision,omitempty"`
	// Built is when the binary was built, in RFC 3339 format, if stamped
	// (see built). The commit itself is the reference for everything else
	// about the source, including its time.
	Built string `json:"built,omitempty"`
	// Modified reports whether the working tree had uncommitted changes.
	Modified bool `json:"modified,omitempty"`
	// Go is the toolchain that built the binary, such as go1.24.2.
	Go string `json:"go,omitempty"`
	// Platform is the GOOS/GOARCH the binary was built for.
	Platform string `json:"platform,omitempty"`
	// Downloaded reports a build from a module Go downloaded and checked
	// against its checksum, as go run and go install build module@v0.0.1,
	// rather than from a checkout. It describes the running binary only, so
	// it is not part of an installed version's record.
	Downloaded bool `json:"-"`
}

// Current returns the running binary's Info.
func Current() Info {
	info := Info{Version: Devel, Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if build, ok := debug.ReadBuildInfo(); ok {
		info = fromBuild(build, info)
	}
	if stamped != "" {
		info.Version = stamped
	}
	// Only a well-formed time is reported, so a stray -X value cannot put
	// arbitrary text into version output or a build record.
	if _, err := time.Parse(time.RFC3339, built); err == nil {
		info.Built = built
	}
	return info
}

// fromBuild returns info with the details build records filled in, or info as
// it is when build is nil.
func fromBuild(build *debug.BuildInfo, info Info) Info {
	if build == nil {
		return info
	}
	if v := build.Main.Version; v != "" && v != Devel {
		info.Version = v
	}
	// Go records the main module's checksum only when it downloaded the
	// module; a build in a checkout has none.
	info.Downloaded = build.Main.Sum != ""
	var goos, goarch string
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.modified":
			info.Modified = setting.Value == "true"
		case "GOOS":
			goos = setting.Value
		case "GOARCH":
			goarch = setting.Value
		}
	}
	if goos != "" && goarch != "" {
		info.Platform = goos + "/" + goarch
	}
	return info
}

// Summary describes the build in one line, such as
// "commit 0123456789ab (modified), built 2026-10-25T12:00:00Z, go1.24.2,
// linux/amd64". Unknown details are left out.
func (i Info) Summary() string {
	var parts []string
	if commit := i.commit(); commit != "" {
		parts = append(parts, "commit "+commit)
	}
	if i.Built != "" {
		parts = append(parts, "built "+i.Built)
	}
	for _, part := range []string{i.Go, i.Platform} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

// commit returns the commit abbreviated to 12 characters, as Go's
// pseudo-versions abbreviate it, marked when the tree was modified, or "".
func (i Info) commit() string {
	if i.Revision == "" {
		return ""
	}
	commit := i.Revision[:min(len(i.Revision), 12)]
	if i.Modified {
		commit += " (modified)"
	}
	return commit
}

// Details describes the build on several lines, one per known detail, for
// `homeskillet version`, which adds the platform lines itself.
func (i Info) Details() string {
	var b strings.Builder
	for _, line := range [][2]string{{"commit", i.commit()}, {"built", i.Built}, {"go", i.Go}} {
		if line[1] != "" {
			fmt.Fprintf(&b, "  %-10s %s\n", line[0]+":", line[1])
		}
	}
	return b.String()
}

// SameSource reports whether i and other are builds of the same version from
// the same unmodified commit, with the same toolchain, for the same platform.
// Such builds differ only in when they were built, so one may stand for the
// other. A build without a recorded commit, such as a stamped container
// build, is never the same source as anything: nothing proves it.
func (i Info) SameSource(other Info) bool {
	return i.Revision != "" && !i.Modified && !other.Modified && i.Version == other.Version &&
		i.Revision == other.Revision && i.Go == other.Go && i.Platform == other.Platform
}

// semver is a version split into its parts by parse.
type semver struct {
	core  [3]string // major, minor, and patch, as decimal strings
	pre   []string  // prerelease identifiers; nil means none
	build []string  // build metadata identifiers; nil means none
}

// parse splits v, a Semantic Versioning 2.0.0 version with Go's leading "v",
// into its parts, reporting whether it is valid. A valid version holds only
// ASCII letters, digits, '.', '-', and '+', and none of its identifiers is
// empty, so it can never be "." or "..", contain a "/", or rewrite a
// terminal. That makes it safe to use as a directory name and to print.
func parse(v string) (semver, bool) {
	rest, ok := strings.CutPrefix(v, "v")
	if !ok {
		return semver{}, false
	}
	var p semver
	// Build metadata starts at the first '+'. The prerelease starts at the
	// first '-' before it; prerelease identifiers may contain '-' themselves.
	rest, build, hasBuild := strings.Cut(rest, "+")
	core, pre, hasPre := strings.Cut(rest, "-")
	numbers := strings.Split(core, ".")
	if len(numbers) != len(p.core) {
		return semver{}, false
	}
	for i, n := range numbers {
		if !number(n) {
			return semver{}, false
		}
		p.core[i] = n
	}
	if hasPre {
		p.pre = strings.Split(pre, ".")
		for _, id := range p.pre {
			if !identifier(id) || numeric(id) && !number(id) {
				return semver{}, false
			}
		}
	}
	if hasBuild {
		p.build = strings.Split(build, ".")
		for _, id := range p.build {
			if !identifier(id) {
				return semver{}, false
			}
		}
	}
	return p, true
}

// Valid reports whether v is a semantic version with a leading "v", such as
// v0.0.1, v0.0.1-rc.1, or v0.0.1+dirty.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

// Dirty reports whether v carries Go's +dirty build metadata, meaning it was
// built from a tree with uncommitted changes and does not name one exact
// build.
func Dirty(v string) bool {
	p, ok := parse(v)
	return ok && slices.Contains(p.build, "dirty")
}

// Compare orders a and b by Semantic Versioning 2.0.0 precedence, returning
// -1, 0, or +1. Build metadata does not affect precedence, so v0.0.1 and
// v0.0.1+dirty compare equal. Invalid versions sort before valid ones.
func Compare(a, b string) int {
	pa, okA := parse(a)
	pb, okB := parse(b)
	if !okA || !okB {
		return cmp.Compare(boolInt(okA), boolInt(okB))
	}
	for i := range pa.core {
		if c := compareNumbers(pa.core[i], pb.core[i]); c != 0 {
			return c
		}
	}
	// A version without a prerelease ranks above any prerelease of it.
	switch {
	case pa.pre == nil && pb.pre == nil:
		return 0
	case pa.pre == nil:
		return 1
	case pb.pre == nil:
		return -1
	default:
		return slices.CompareFunc(pa.pre, pb.pre, compareIdentifiers)
	}
}

// compareIdentifiers orders prerelease identifiers: numeric ones by value,
// alphanumeric ones in ASCII order, and numeric ones before alphanumeric ones.
func compareIdentifiers(a, b string) int {
	na, nb := numeric(a), numeric(b)
	switch {
	case na && nb:
		return compareNumbers(a, b)
	case na != nb:
		return cmp.Compare(boolInt(!na), boolInt(!nb))
	default:
		return strings.Compare(a, b)
	}
}

// compareNumbers orders decimal strings without leading zeros by value. A
// longer string is larger, so values beyond any integer type still compare.
func compareNumbers(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

// identifier reports whether s is a nonempty run of ASCII letters, digits,
// and hyphens.
func identifier(s string) bool {
	return s != "" && strings.Trim(s, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-") == ""
}

// numeric reports whether s is a nonempty run of ASCII digits.
func numeric(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// number reports whether s is a decimal number without leading zeros, as
// version cores and numeric prerelease identifiers must be.
func number(s string) bool {
	return numeric(s) && (s == "0" || s[0] != '0')
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
