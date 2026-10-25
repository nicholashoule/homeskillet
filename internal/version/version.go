// Package version reports which build of homeskillet is running, reads the
// same details from other homeskillet binaries without running them, and
// orders version strings so installations can be compared.
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
	"debug/buildinfo"
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
)

// stamped overrides the recorded module version when set at link time.
var stamped string

// Devel is the version of a build that records none.
const Devel = "(devel)"

// Info describes one homeskillet build.
type Info struct {
	// Version is a semantic version with a leading "v", or Devel.
	Version string
	// Revision is the commit the binary was built from, if recorded.
	Revision string
	// Time is that commit's time in RFC 3339 format, if recorded. Go records
	// no build time, which keeps builds reproducible.
	Time string
	// Modified reports whether the working tree had uncommitted changes.
	Modified bool
	// Go is the toolchain that built the binary, such as go1.24.2.
	Go string
	// Platform is the GOOS/GOARCH the binary was built for.
	Platform string
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
	return info
}

// Read returns the Info recorded in the Go binary at path without running
// it. A version stamped at link time is not part of that record, so Version
// is the module version Go recorded.
func Read(path string) (Info, error) {
	build, err := buildinfo.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	return fromBuild(build, Info{Version: Devel, Go: build.GoVersion}), nil
}

func fromBuild(build *debug.BuildInfo, info Info) Info {
	if v := build.Main.Version; v != "" && v != Devel {
		info.Version = v
	}
	var goos, goarch string
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.time":
			info.Time = setting.Value
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
// "commit 0123456789ab (modified), go1.24.2, linux/amd64". Unknown details
// are left out.
func (i Info) Summary() string {
	var parts []string
	if i.Revision != "" {
		commit := "commit " + i.Revision[:min(len(i.Revision), 12)]
		if i.Modified {
			commit += " (modified)"
		}
		parts = append(parts, commit)
	}
	for _, part := range []string{i.Go, i.Platform} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

// Details describes the build on several lines, one per known detail, for
// `homeskillet version`.
func (i Info) Details() string {
	var b strings.Builder
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "  %-10s %s\n", label+":", value)
		}
	}
	revision := i.Revision
	if revision != "" && i.Modified {
		revision += " (uncommitted changes)"
	}
	line("commit", revision)
	line("committed", i.Time)
	line("go", i.Go)
	line("platform", i.Platform)
	return b.String()
}

// semver matches Semantic Versioning 2.0.0 with Go's leading "v": a
// MAJOR.MINOR.PATCH core without leading zeros, an optional -prerelease, and
// optional +build metadata. Only ASCII letters, digits, '.', '-', and '+' can
// match, and no identifier is empty, so a valid version can never be "." or
// "..", contain a "/", or rewrite a terminal. That makes it safe to use as a
// directory name and to print.
var semver = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
	`(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?` +
	`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Valid reports whether v is a semantic version with a leading "v", such as
// v0.0.1, v0.0.1-rc.1, or v0.0.1+dirty.
func Valid(v string) bool {
	return semver.MatchString(v)
}

// Dirty reports whether v carries Go's +dirty build metadata, meaning it was
// built from a tree with uncommitted changes and does not name one exact
// build.
func Dirty(v string) bool {
	_, build, found := strings.Cut(v, "+")
	return found && Valid(v) && slices.Contains(strings.Split(build, "."), "dirty")
}

// Compare orders a and b by Semantic Versioning 2.0.0 precedence, returning
// -1, 0, or +1. Build metadata does not affect precedence, so v0.0.1 and
// v0.0.1+dirty compare equal. Invalid versions sort before valid ones.
func Compare(a, b string) int {
	ma, mb := semver.FindStringSubmatch(a), semver.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return cmp.Compare(boolInt(ma != nil), boolInt(mb != nil))
	}
	for i := 1; i <= 3; i++ {
		if c := compareNumbers(ma[i], mb[i]); c != 0 {
			return c
		}
	}
	// A version without a prerelease ranks above any prerelease of it.
	switch pa, pb := ma[4], mb[4]; {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	default:
		return slices.CompareFunc(strings.Split(pa, "."), strings.Split(pb, "."), compareIdentifiers)
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

func numeric(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
