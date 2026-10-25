package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// call runs the command, capturing stdout and stderr separately.
func call(args ...string) (rc int, stdout, stderr string) {
	var o, e bytes.Buffer
	rc = run(args, &o, &e)
	return rc, o.String(), e.String()
}

func TestMkdirIdempotentAndNested(t *testing.T) {
	d := filepath.Join(t.TempDir(), "a", "b", "build")
	if rc, _, e := call("mkdir", d); rc != 0 {
		t.Fatalf("mkdir rc=%d stderr=%q", rc, e)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	// Idempotent: a second call on the existing dir is a silent success.
	if rc, _, e := call("mkdir", d); rc != 0 || e != "" {
		t.Fatalf("mkdir not idempotent: rc=%d stderr=%q", rc, e)
	}
}

func TestRmrfMultipleAndAbsentIsSilent(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "build")
	file := filepath.Join(tmp, "coverage.out")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mix of an existing dir, an existing file, and an absent path -> all ok.
	if rc, _, e := call("rmrf", dir, file, filepath.Join(tmp, "missing")); rc != 0 {
		t.Fatalf("rmrf rc=%d stderr=%q", rc, e)
	}
	for _, p := range []string{dir, file} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err=%v)", p, err)
		}
	}
}

func TestCpCreatesParentAndIsExecutable(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "pre-commit")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, ".git", "hooks", "pre-commit") // parent absent
	if rc, _, e := call("cp", src, dst); rc != 0 {
		t.Fatalf("cp rc=%d stderr=%q", rc, e)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !strings.Contains(string(got), "echo hi") {
		t.Fatalf("dst content wrong: %q err=%v", got, err)
	}
	// The executable bit only exists on POSIX; Windows reports -rw-rw-rw-
	// regardless (git there does not need it). The cp intent ("runnable
	// hook on POSIX") is only assertable off Windows.
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dst); fi != nil && fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("dst not executable on %s: mode=%v", runtime.GOOS, fi.Mode())
		}
	}
}

// cp over an installed hook that lost its executable bit marks it
// executable again: OpenFile's mode applies only when it creates the file,
// and git silently skips a non-executable hook.
func TestCpMarksAnExistingDestinationExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable bit exists on POSIX only")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "pre-commit")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("#!/bin/sh\necho old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o644); err != nil { // past the umask
		t.Fatal(err)
	}
	if rc, _, e := call("cp", src, dst); rc != 0 {
		t.Fatalf("cp rc=%d stderr=%q", rc, e)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("dst mode = %v; want -rwxr-xr-x", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(dst); !strings.Contains(string(got), "echo new") {
		t.Errorf("dst content = %q; want the new hook", got)
	}
}

func TestRequirePresentAndMissing(t *testing.T) {
	tmp := t.TempDir()
	present := filepath.Join(tmp, ".env")
	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if rc, _, e := call("require", present, "should not print"); rc != 0 || e != "" {
		t.Fatalf("require(present) rc=%d stderr=%q, want 0/empty", rc, e)
	}
	rc, _, e := call("require", filepath.Join(tmp, "nope"), "ERROR: .env missing")
	if rc != 1 || !strings.Contains(e, "ERROR: .env missing") {
		t.Fatalf("require(missing) rc=%d stderr=%q, want 1 + message", rc, e)
	}
}

func TestHelpRendersTargetsAndGroups(t *testing.T) {
	mf := filepath.Join(t.TempDir(), "Makefile")
	const body = "" +
		"##@ Build\n" +
		"build: ## Build the binary\n" +
		"\t@go build ./...\n" +
		".PHONY: build\n" + // must be skipped (leading dot, no '## ')
		"clean: deps ## Remove artifacts\n" +
		"# not-a-target: ## comment line, not a rule\n"
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, e := call("help", mf)
	if rc != 0 || e != "" {
		t.Fatalf("help rc=%d stderr=%q", rc, e)
	}
	if !strings.Contains(out, "Build\n") {
		t.Errorf("missing ##@ group header; got:\n%s", out)
	}
	if !strings.Contains(out, "build") || !strings.Contains(out, "Build the binary") {
		t.Errorf("missing documented target; got:\n%s", out)
	}
	if !strings.Contains(out, "clean") || !strings.Contains(out, "Remove artifacts") {
		t.Errorf("missing second target; got:\n%s", out)
	}
	if strings.Contains(out, "not-a-target") || strings.Contains(out, ".PHONY") {
		t.Errorf("non-target lines leaked into help; got:\n%s", out)
	}
	// A missing makefile is a hard error (exit 1), not silent.
	if rc, _, _ := call("help", filepath.Join(t.TempDir(), "nope")); rc != 1 {
		t.Errorf("help(missing file) rc=%d, want 1", rc)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"mkdir"},
		{"mkdir", ""},
		{"rmrf"},
		{"cp", "only-one"},
		{"require", "one-arg"},
		{"help"},
		{"bogus"},
	}
	for _, args := range cases {
		if rc, _, e := call(args...); rc != 2 {
			t.Errorf("run(%v) rc=%d, want 2 (usage); stderr=%q", args, rc, e)
		}
	}
}

// writeFile is a one-line helper used by the env-merge tests; matches
// the style used elsewhere in this file (no helper factored out for one
// existing caller, so just inlined here).
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// readFileT reads + returns a file's contents as a string for assert
// helpers; t.Fatal on error keeps the call sites tight.
func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(b)
}

// TestEnvMerge_CreatesDstWhenAbsent: the dev-test workflow assumes
// `xplat env-merge dev-masters/PLATFORM_ANCHORS_B64.env .env` works even
// when .env hasn't been created yet. Pinning this contract so a fresh
// clone runs the loop cleanly without an extra `touch .env` step.
func TestEnvMerge_CreatesDstWhenAbsent(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "FOO=bar\nBAZ=qux\n")

	if rc, _, e := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge rc=%d stderr=%q", rc, e)
	}
	got := readFileT(t, dst)
	if !strings.Contains(got, "FOO=bar\n") || !strings.Contains(got, "BAZ=qux\n") {
		t.Errorf("dst missing expected keys; got %q", got)
	}
}

// TestEnvMerge_ReplacesInPlace: existing dst keys are updated in
// SOURCE-FILE position, not appended. The load-bearing property is
// that re-running the merge yields a stable diff -- otherwise every
// dev-test cycle would append a fresh copy of PLATFORM_ANCHORS_B64 to
// .env and the file would grow unbounded.
func TestEnvMerge_ReplacesInPlace(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "PLATFORM_ANCHORS_B64=NEW_VALUE\n")
	writeFile(t, dst, "FOO=keep\nPLATFORM_ANCHORS_B64=OLD_VALUE\nBAR=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	// New value is in.
	if !strings.Contains(got, "PLATFORM_ANCHORS_B64=NEW_VALUE\n") {
		t.Errorf("new value not applied; got %q", got)
	}
	// Old value is gone.
	if strings.Contains(got, "OLD_VALUE") {
		t.Errorf("old value not replaced; got %q", got)
	}
	// Neighboring keys preserved.
	if !strings.Contains(got, "FOO=keep\n") || !strings.Contains(got, "BAR=keep\n") {
		t.Errorf("neighboring keys lost; got %q", got)
	}
	// Position preserved: PLATFORM_ANCHORS_B64 must still be the SECOND
	// non-empty line, not appended at the end. Otherwise the file's
	// natural ordering decays across re-runs.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[1], "PLATFORM_ANCHORS_B64=") {
		t.Errorf("PLATFORM_ANCHORS_B64 moved; lines=%q", lines)
	}
}

// TestEnvMerge_AppendsNewKeys: src keys not present in dst are added
// at the END, in src-file order. Two src keys appended in different
// orders produce different appended-order dst files.
func TestEnvMerge_AppendsNewKeys(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "ALPHA=1\nBETA=2\n")
	writeFile(t, dst, "EXISTING=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "EXISTING=keep" || lines[1] != "ALPHA=1" || lines[2] != "BETA=2" {
		t.Errorf("append order wrong; got lines=%q", lines)
	}
}

// TestEnvMerge_PreservesCommentsAndBlanks: comments + blank lines in
// dst must round-trip verbatim. The .env files this targets are
// often hand-edited with operator-readable comments that would lose
// their context if reshuffled.
func TestEnvMerge_PreservesCommentsAndBlanks(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "FOO=updated\n")
	writeFile(t, dst, "# this is a comment\nFOO=old\n\n# trailing comment\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if !strings.Contains(got, "# this is a comment\n") {
		t.Errorf("leading comment lost; got %q", got)
	}
	if !strings.Contains(got, "# trailing comment\n") {
		t.Errorf("trailing comment lost; got %q", got)
	}
	if !strings.Contains(got, "FOO=updated\n") {
		t.Errorf("value not applied; got %q", got)
	}
}

// TestEnvMerge_ValueWithEqualsRoundTrips: a value containing '=' (URLs
// with query strings, base64 padding, etc.) must round-trip intact.
// The PLATFORM_ANCHORS_B64 case carries comma-separated base64 values
// where each segment may end in '='; if we split on '=' naively the
// value would be truncated.
func TestEnvMerge_ValueWithEqualsRoundTrips(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	tricky := "AAAA=,BBBB==,CCCC=DDDD="
	writeFile(t, src, "PLATFORM_ANCHORS_B64="+tricky+"\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if !strings.Contains(got, "PLATFORM_ANCHORS_B64="+tricky+"\n") {
		t.Errorf("value with '=' truncated; got %q", got)
	}
}

// TestEnvMerge_EmptySrcIsNoop: a src with no KEY=VALUE lines is a
// silent success -- relevant when an operator runs the merge before
// they've actually generated the source bundle.
func TestEnvMerge_EmptySrcIsNoop(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "# just a comment\n\n")
	writeFile(t, dst, "EXISTING=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge rc=%d", rc)
	}
	got := readFileT(t, dst)
	if got != "EXISTING=keep\n" {
		t.Errorf("dst mutated on empty src; got %q", got)
	}
}

// TestEnvMerge_AtomicWriteNoTempLeak: a successful merge must clean
// up its .tmp scratch file. Otherwise repeated runs would leave
// stale .env.tmp files that confuse `git status` and trip the
// operator into committing them.
func TestEnvMerge_AtomicWriteNoTempLeak(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "FOO=bar\n")
	writeFile(t, dst, "EXISTING=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge rc=%d", rc)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file leaked: stat err=%v", err)
	}
	// Silence the unused-import linter on `runtime` if a future test
	// removes the only use. (Keep this anchor; some tests above use
	// runtime.GOOS for platform-conditional skips.)
	_ = runtime.GOOS
}

// TestEnvMerge_NormalizesCRLFToLF: project policy (see .gitattributes)
// is LF for every text file, so a Windows-host .env that arrived with
// CRLF gets normalized on first merge. The original CRLF input must
// parse cleanly before being re-emitted as pure LF -- no CR bytes
// survive in the output.
func TestEnvMerge_NormalizesCRLFToLF(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "PLATFORM_ANCHORS_B64=new\n")
	// dst arrives CRLF-terminated throughout (Notepad on Windows).
	writeFile(t, dst, "FOO=keep\r\nPLATFORM_ANCHORS_B64=old\r\nBAR=keep\r\n")

	if rc, _, e := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge rc=%d stderr=%q", rc, e)
	}
	got := readFileT(t, dst)
	// No CR bytes should survive: all line endings are LF.
	if strings.Contains(got, "\r") {
		t.Errorf("CR byte survived; output is not pure LF: %q", got)
	}
	// Three LF-terminated lines.
	if strings.Count(got, "\n") != 3 {
		t.Errorf("expected 3 LF terminators; got %d in %q", strings.Count(got, "\n"), got)
	}
	// Merged value landed (and LF-terminated, not CRLF).
	if !strings.Contains(got, "PLATFORM_ANCHORS_B64=new\n") {
		t.Errorf("merged value missing; got %q", got)
	}
	// Neighboring keys preserved (and now LF-terminated, no \r).
	if !strings.Contains(got, "FOO=keep\n") || !strings.Contains(got, "BAR=keep\n") {
		t.Errorf("neighboring keys lost or wrong-terminated; got %q", got)
	}
}

// TestEnvMerge_LFInputStaysLF: the converse of the CRLF test -- a
// pure-LF dst must NOT have CRLF introduced. Cross-platform CI
// runners typically check in LF files; surprising them with CRLF
// would break git's autocrlf detection and produce noisy diffs.
func TestEnvMerge_LFInputStaysLF(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "FOO=updated\n")
	writeFile(t, dst, "FOO=old\nBAR=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if strings.Contains(got, "\r") {
		t.Errorf("CRLF introduced into LF file; got %q", got)
	}
}

// TestEnvMerge_HeaderInsertedBeforeAppend: --header emits a section
// divider comment on the line BEFORE the first newly-appended key,
// so a freshly-merged group is visually grouped in .env.
func TestEnvMerge_HeaderInsertedBeforeAppend(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "PLATFORM_ANCHORS_B64=value\n")
	writeFile(t, dst, "EXISTING=keep\n")

	if rc, _, _ := call("env-merge", "--header", "# ----- Anchors -----", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (existing + header + appended); got %d in %q", len(lines), got)
	}
	if lines[0] != "EXISTING=keep" || lines[1] != "# ----- Anchors -----" || !strings.HasPrefix(lines[2], "PLATFORM_ANCHORS_B64=") {
		t.Errorf("layout wrong; got %q", lines)
	}
}

// TestEnvMerge_HeaderSkippedOnInPlaceReplace: when every src key is
// already in dst, no header is emitted -- the file's existing
// structure is untouched. Pure-replace runs shouldn't grow .env with
// stray divider comments.
func TestEnvMerge_HeaderSkippedOnInPlaceReplace(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "PLATFORM_ANCHORS_B64=new\n")
	writeFile(t, dst, "PLATFORM_ANCHORS_B64=old\n")

	if rc, _, _ := call("env-merge", "--header", "# ----- Anchors -----", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if strings.Contains(got, "----- Anchors -----") {
		t.Errorf("header emitted on pure in-place replace; got %q", got)
	}
	if !strings.Contains(got, "PLATFORM_ANCHORS_B64=new\n") {
		t.Errorf("value not replaced; got %q", got)
	}
}

// TestEnvMerge_HeaderIdempotent: a re-run with the same --header
// against a dst that already carries that header doesn't append a
// duplicate. Important so `make dev-augment-rebuild` re-runs don't
// pile up section dividers.
func TestEnvMerge_HeaderIdempotent(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	header := "# ----- Anchors -----"
	writeFile(t, src, "ALPHA=1\n")
	writeFile(t, dst, "EXISTING=keep\n"+header+"\nBETA=2\n")

	if rc, _, _ := call("env-merge", "--header", header, src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	// Exactly one occurrence of the header survived.
	if c := strings.Count(got, header); c != 1 {
		t.Errorf("header duplicated; want 1, got %d in %q", c, got)
	}
	// And the new key was still appended.
	if !strings.Contains(got, "ALPHA=1\n") {
		t.Errorf("appended key missing; got %q", got)
	}
}

// TestEnvMerge_HeaderOnlyOncePerRun: a src with TWO new keys yields
// ONE header line, not one before each appended key. The header
// marks the SECTION, not each entry within it.
func TestEnvMerge_HeaderOnlyOncePerRun(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "ALPHA=1\nBETA=2\nGAMMA=3\n")
	writeFile(t, dst, "EXISTING=keep\n")

	if rc, _, _ := call("env-merge", "--header", "# section", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if c := strings.Count(got, "# section\n"); c != 1 {
		t.Errorf("header emitted %d times; want 1 in %q", c, got)
	}
}

// TestEnvMerge_CRLFSrcDoesNotLeakIntoDst pins the readEnvFile leg of
// the LF-everywhere policy: a Windows-host src file (CRLF) merged
// into a clean LF dst MUST yield a pure-LF dst, with the value
// captured cleanly (no trailing \r riding along in the captured
// VALUE field). Without normalization the regex would capture
// "bundle-value\r" and write it straight back.
func TestEnvMerge_CRLFSrcDoesNotLeakIntoDst(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "PLATFORM_ANCHORS_B64=clean-value\r\n")
	writeFile(t, dst, "FOO=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if strings.Contains(got, "\r") {
		t.Errorf("CR leaked from CRLF src into LF dst: %q", got)
	}
	// Value captured cleanly (no trailing \r riding along).
	if !strings.Contains(got, "PLATFORM_ANCHORS_B64=clean-value\n") {
		t.Errorf("value mis-captured; got %q", got)
	}
}

// TestDos2Unix_StripsCRLF: the headline use case -- a file pasted
// from a Windows editor becomes LF-only after one invocation.
func TestDos2Unix_StripsCRLF(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.txt")
	writeFile(t, p, "alpha\r\nbeta\r\ngamma\r\n")

	if rc, _, e := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix rc=%d stderr=%q", rc, e)
	}
	got := readFileT(t, p)
	if got != "alpha\nbeta\ngamma\n" {
		t.Errorf("dos2unix output mismatch; got %q", got)
	}
}

// TestDos2Unix_Idempotent: re-running on a pure-LF file is a
// content no-op. We assert the mtime stays exactly the same -- that's
// what make's incremental-build logic depends on.
func TestDos2Unix_Idempotent(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.txt")
	writeFile(t, p, "alpha\nbeta\n")
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	if rc, _, _ := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix failed")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime advanced on pure-LF input (idempotent contract broken); before=%v after=%v", before.ModTime(), after.ModTime())
	}
}

// TestDos2Unix_MultipleFiles: variadic operator-call shape. One bad
// path in the middle stops processing immediately so the operator
// gets a clear "file N failed" signal rather than partial success.
func TestDos2Unix_MultipleFiles(t *testing.T) {
	d := t.TempDir()
	a := filepath.Join(d, "a.txt")
	b := filepath.Join(d, "b.txt")
	writeFile(t, a, "alpha\r\n")
	writeFile(t, b, "beta\r\n")
	if rc, _, e := call("dos2unix", a, b); rc != 0 {
		t.Fatalf("dos2unix rc=%d stderr=%q", rc, e)
	}
	if got := readFileT(t, a); got != "alpha\n" {
		t.Errorf("a mismatch; got %q", got)
	}
	if got := readFileT(t, b); got != "beta\n" {
		t.Errorf("b mismatch; got %q", got)
	}
}

// TestDos2Unix_PreservesBareCR: a bare CR (Mac Classic line ending,
// or a CR byte inside binary content) is NOT touched -- we strip
// CRLF specifically, not all CRs. Stripping bare CRs would risk
// corrupting files that happen to carry 0x0D bytes outside a line-
// ending context.
func TestDos2Unix_PreservesBareCR(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.txt")
	writeFile(t, p, "alpha\rbeta\n")

	if rc, _, _ := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix failed")
	}
	got := readFileT(t, p)
	if got != "alpha\rbeta\n" {
		t.Errorf("bare CR was stripped (should be preserved); got %q", got)
	}
}

// TestDos2Unix_RejectsNoArgs: usage error on no args, exit 2.
func TestDos2Unix_RejectsNoArgs(t *testing.T) {
	if rc, _, e := call("dos2unix"); rc != 2 {
		t.Errorf("expected rc=2 (usage); got rc=%d stderr=%q", rc, e)
	}
}

// bomPrefix is the UTF-8 byte-order-mark (EF BB BF). Built from an
// escape sequence rather than a literal U+FEFF in source because Go
// rejects literal BOMs in source files ("illegal byte order mark"),
// which is the same property at the Go-tooling layer that this
// dos2unix helper exists to enforce at the file-IO layer.
const bomPrefix = "\ufeff"

// TestDos2Unix_StripsUTF8BOM: a Windows editor saving with the "include
// BOM" option produces files where bash `source file` reads the BOM
// as part of the first key name, and docker-compose treats the
// BOM-prefixed key as distinct from the unprefixed one. The BOM is
// invisible in editors so debugging is brutal. dos2unix strips it.
func TestDos2Unix_StripsUTF8BOM(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.env")
	writeFile(t, p, bomPrefix+"FOO=bar\n")
	if rc, _, _ := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix failed")
	}
	got := readFileT(t, p)
	if got != "FOO=bar\n" {
		t.Errorf("BOM not stripped; got %q", got)
	}
}

// TestDos2Unix_StripsBOMAndCRLF: both hazards in one file produce
// one clean output. Pins that the two normalizations compose.
func TestDos2Unix_StripsBOMAndCRLF(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.env")
	writeFile(t, p, bomPrefix+"FOO=bar\r\nBAZ=qux\r\n")
	if rc, _, _ := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix failed")
	}
	got := readFileT(t, p)
	if got != "FOO=bar\nBAZ=qux\n" {
		t.Errorf("combined normalize broken; got %q", got)
	}
}

// TestDos2Unix_PreservesInteriorBOMLikeBytes: only the BOM at file
// start is stripped. A U+FEFF sequence later in the file is almost
// certainly a legitimate UTF-8 char (zero-width no-break space)
// rather than a misplaced BOM; stripping it would corrupt data.
func TestDos2Unix_PreservesInteriorBOMLikeBytes(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.txt")
	body := "FOO=bar\n" + bomPrefix + " is U+FEFF used legitimately\n"
	writeFile(t, p, body)
	if rc, _, _ := call("dos2unix", p); rc != 0 {
		t.Fatalf("dos2unix failed")
	}
	got := readFileT(t, p)
	if got != body {
		t.Errorf("interior U+FEFF stripped (should be preserved); got %q", got)
	}
}

// TestEnvMerge_StripsBOMFromDst: cross-OS hardening on the env-merge
// path: if a Windows editor saved .env with a BOM, the next merge
// auto-heals. Self-healing matters because BOMs are invisible in
// editors and almost never spotted before they break a build.
func TestEnvMerge_StripsBOMFromDst(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.env")
	dst := filepath.Join(d, "dst.env")
	writeFile(t, src, "NEW=value\n")
	// dst arrives with a UTF-8 BOM (Notepad's "UTF-8 with BOM" save).
	writeFile(t, dst, bomPrefix+"EXISTING=keep\n")

	if rc, _, _ := call("env-merge", src, dst); rc != 0 {
		t.Fatalf("env-merge failed")
	}
	got := readFileT(t, dst)
	if strings.HasPrefix(got, bomPrefix) {
		t.Errorf("BOM survived env-merge; got %q", got)
	}
	if !strings.Contains(got, "EXISTING=keep\n") || !strings.Contains(got, "NEW=value\n") {
		t.Errorf("content lost; got %q", got)
	}
}
