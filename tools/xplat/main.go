// Command xplat is a tiny cross-platform shim for the filesystem operations
// Makefiles need, so recipes contain ZERO shell-specific syntax.
//
// CANONICAL SOURCE: github.com/nicholashoule/go-webapp tools/xplat (xplat-v3).
// Vendored verbatim into sibling repos (no shared module on purpose: a
// ~120-line build shim should not add a versioned build-time dependency).
// Keep copies byte-identical; bump the xplat-vN tag in this line when the
// behavior changes so drift is greppable across repos.
//
// Make invokes `go run ./tools/xplat <cmd> ...` directly (never a shell
// builtin), so the same recipe works identically under cmd.exe, PowerShell,
// and POSIX sh. This replaces the fragile, recurring `ifeq ($(OS)...)` /
// `$(findstring sh,$(SHELL))` cmd-vs-sh dual recipes (mkdir -p vs
// "if not exist", rm -rf vs "rmdir /s /q", cp vs "copy /Y", test -f vs
// "if not exist") that broke on Windows ("The syntax of the command is
// incorrect.").
//
// Subcommands:
//
//	mkdir <dir>              os.MkdirAll (idempotent; silent if it exists)
//	rmrf <path>...           os.RemoveAll each (idempotent; silent if absent)
//	cp <src> <dst>           copy a file, dst mode 0o755 (carries the
//	                         executable bit POSIX git hooks need; a no-op
//	                         distinction on Windows)
//	require <path> <message> exit 1 with <message> if <path> is missing,
//	                         else exit 0 (build-precondition guard)
//	help <makefile>...       render the `## ` help screen (replaces the
//	                         awk-vs-powershell HELP_CMD branch, which
//	                         broke under sh-on-Windows); `##@ group`
//	                         headers supported, plain text (no ANSI)
//	silent <cmd> [args...]   run <cmd> with the given args; stream stdout
//	                         to our stdout, DISCARD stderr; exit 0 always
//	                         (the caller's $(or ...) handles empty stdout
//	                         as the failure signal). Lets Makefile
//	                         $(shell ...) substitutions stay quiet
//	                         without using cmd-only `2>NUL` or sh-only
//	                         `2>/dev/null` -- e.g. silencing git's
//	                         "fatal: No names found" on a no-tags repo.
//	env-merge [--header <text>] <src> <dst>
//	                         merge KEY=VALUE lines from <src> into <dst>:
//	                         every src key replaces the existing dst line
//	                         in place (preserving order); src keys not yet
//	                         present in dst are appended. Comments + blank
//	                         lines in dst are preserved verbatim. Creates
//	                         dst if it doesn't exist. Replaces the
//	                         "PowerShell Get-Content | Where-Object |
//	                         Add-Content vs sh grep+awk" branch for
//	                         operator-driven .env updates. Input is
//	                         normalized through dos2unix on read so a
//	                         CRLF src cannot leak into the LF dst.
//	                         --header inserts a section-divider comment
//	                         line BEFORE the first newly-appended key,
//	                         and ONLY if (a) keys are actually appended
//	                         (no header on a pure in-place replace) AND
//	                         (b) dst doesn't already contain that exact
//	                         header line (idempotent across re-runs).
//	dos2unix <path>...       normalize each file for cross-OS use in
//	                         place (atomic write):
//	                           - strip CR before LF (DOS->Unix line
//	                             endings, project .gitattributes policy);
//	                           - strip a single leading UTF-8 BOM
//	                             (Windows editors' "save with BOM"
//	                             option breaks bash source + docker-
//	                             compose env-file parsing).
//	                         Idempotent. Reuse on any text file an
//	                         operator pasted from a Windows editor
//	                         before it goes into git or gets read by
//	                         a *nix shell.
//
// Exit codes: 0 success, 1 operation failure, 2 usage error.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// emit writes a line to w, discarding the write error: this is a build
// helper writing diagnostics to stderr, where a failed write is both
// impossible in practice and not actionable (satisfies errcheck without
// noise at every call site).
func emit(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		emit(stderr, "usage: xplat <mkdir|rmrf|cp|require|help|silent|env-merge|dos2unix> ...")
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "mkdir":
		if len(rest) != 1 || rest[0] == "" {
			emit(stderr, "usage: xplat mkdir <dir>")
			return 2
		}
		if err := os.MkdirAll(rest[0], 0o755); err != nil {
			emit(stderr, "xplat mkdir:", err)
			return 1
		}
		return 0

	case "rmrf":
		if len(rest) == 0 {
			emit(stderr, "usage: xplat rmrf <path>...")
			return 2
		}
		for _, p := range rest {
			if err := os.RemoveAll(p); err != nil {
				emit(stderr, "xplat rmrf:", err)
				return 1
			}
		}
		return 0

	case "cp":
		if len(rest) != 2 || rest[0] == "" || rest[1] == "" {
			emit(stderr, "usage: xplat cp <src> <dst>")
			return 2
		}
		if err := copyFile(rest[0], rest[1]); err != nil {
			emit(stderr, "xplat cp:", err)
			return 1
		}
		return 0

	case "require":
		if len(rest) != 2 {
			emit(stderr, "usage: xplat require <path> <message>")
			return 2
		}
		if _, err := os.Stat(rest[0]); err != nil {
			emit(stderr, rest[1])
			return 1
		}
		return 0

	case "help":
		if len(rest) == 0 {
			emit(stderr, "usage: xplat help <makefile>...")
			return 2
		}
		return printHelp(stdout, stderr, rest)

	case "silent":
		// Run a subcommand, stream its stdout to ours, DISCARD its
		// stderr, and always exit 0. The caller (Make's $(shell ...)
		// inside $(or ...)) treats empty stdout as the failure signal
		// and falls through to the literal default. This lets
		// Makefiles silence noisy commands like
		// `git describe --tags --abbrev=0` on a no-tags repo without
		// using cmd-only `2>NUL` or sh-only `2>/dev/null`.
		if len(rest) == 0 {
			emit(stderr, "usage: xplat silent <cmd> [args...]")
			return 2
		}
		c := exec.Command(rest[0], rest[1:]...) //nolint:gosec // operator-supplied build command, not user input
		c.Stdout = stdout
		c.Stderr = io.Discard
		_ = c.Run() // ignored: caller distinguishes via empty stdout
		return 0

	case "env-merge":
		// Hand-parse a single optional --header <text> flag before the
		// positional <src> <dst>. We avoid the standard flag package
		// here because the xplat tool deliberately uses argv as a
		// command/positional dispatcher; introducing a FlagSet would
		// invert the convention.
		header := ""
		for len(rest) >= 2 && rest[0] == "--header" {
			header = rest[1]
			rest = rest[2:]
		}
		if len(rest) != 2 {
			emit(stderr, "usage: xplat env-merge [--header <text>] <src> <dst>")
			return 2
		}
		if err := envMerge(rest[0], rest[1], header, stdout); err != nil {
			emit(stderr, "xplat env-merge:", err)
			return 1
		}
		return 0

	case "dos2unix":
		if len(rest) == 0 {
			emit(stderr, "usage: xplat dos2unix <path>...")
			return 2
		}
		for _, p := range rest {
			if err := dos2unix(p); err != nil {
				emit(stderr, "xplat dos2unix:", p+":", err)
				return 1
			}
		}
		return 0

	default:
		emit(stderr, "xplat: unknown subcommand", cmd)
		return 2
	}
}

// normalizeToLF returns the input with cross-OS hazards stripped:
//
//   - leading UTF-8 BOM (EF BB BF) removed: present when a Windows
//     editor saves a UTF-8 file with the "include byte-order-mark"
//     option, breaks `bash` source/`docker-compose` env-file parsing
//     because the first key gets prefixed with three invisible bytes;
//   - CR-before-LF removed: project policy per .gitattributes.
//
// Bare CR (Mac Classic) is left alone -- those files don't exist in
// our toolchain and stripping them would corrupt e.g. compressed
// blobs that happen to contain 0x0D bytes outside a line-ending
// context. The output is therefore "DOS to Unix + BOM-strip"
// specifically, not "all-CR-to-LF / strip-all-non-ASCII". Idempotent:
// pure-LF-without-BOM input round-trips byte-identical.
func normalizeToLF(in []byte) []byte {
	// Strip a single leading UTF-8 BOM (3 bytes). NOT a loop -- a
	// file with multiple BOMs is corrupt enough that the operator
	// should see the second one survive into the output as a clear
	// signal.
	if len(in) >= 3 && in[0] == 0xEF && in[1] == 0xBB && in[2] == 0xBF {
		in = in[3:]
	}
	return []byte(strings.ReplaceAll(string(in), "\r\n", "\n"))
}

// atomicWrite writes body to path via a temp-file + rename, so a crash
// mid-write can't truncate the destination. The temp lives in the same
// directory as path so the rename stays within one filesystem (cross-
// device rename would fall back to copy+delete and lose atomicity).
// On failure the temp is removed so a re-run starts clean.
func atomicWrite(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dos2unix strips CR-before-LF in path, writing the result atomically.
// Idempotent: a pure-LF file round-trips byte-identical, so a re-run
// is a no-op-equivalent rewrite (the file's mtime advances but the
// content does not). Matches the project's LF-everywhere
// .gitattributes policy; called from the dos2unix subcommand and
// reused inside envMerge so CRLF cannot leak from src into dst.
func dos2unix(path string) error {
	in, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := normalizeToLF(in)
	// Skip the atomic write when nothing changed: keeps mtime stable
	// on already-clean files so `make` doesn't view them as updated
	// and re-run dependent recipes.
	if bytes.Equal(in, out) {
		return nil
	}
	return atomicWrite(path, out)
}

// envLineRe matches a KEY=VALUE line at the start of input. Captures
// the key (group 1) so we can replace-or-append based on whether dst
// already holds a line for that key. VALUE is everything after the
// first '=' through end-of-line, so a value containing '=' (URLs,
// base64 padding, etc.) round-trips intact.
var envLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$`)

// envMerge reads KEY=VALUE pairs from src and applies them to dst:
//
//   - keys already present in dst have their lines replaced in place
//     (preserving line position so the file's existing structure is
//     stable across re-runs);
//   - keys not yet present are appended at the end;
//   - comments (# ...) and blank lines in dst are preserved verbatim;
//   - dst is created if it doesn't exist.
//
// header, when non-empty, is inserted as a comment line BEFORE the
// first newly-appended key, with two conditions: (a) only if any
// key is actually appended (a pure in-place replace doesn't trigger
// the header), and (b) only if dst doesn't already contain that
// exact header line (idempotent -- a re-merge that appends a
// different key won't duplicate the section divider).
//
// Write is atomic (write to a temp file, then rename) so a crash
// mid-write can't corrupt dst -- important when dst is the project's
// .env that the operator's whole compose stack depends on.
func envMerge(srcPath, dstPath, header string, stdout io.Writer) error {
	srcMap, srcOrder, err := readEnvFile(srcPath)
	if err != nil {
		return fmt.Errorf("read src: %w", err)
	}
	if len(srcMap) == 0 {
		emit(stdout, "xplat env-merge:", srcPath, "has no KEY=VALUE lines; nothing to merge")
		return nil
	}

	// Read dst (if it exists) through normalizeToLF so a Windows
	// host's CRLF-terminated .env can't leak \r bytes into the merge
	// output. Project policy is LF for all text files (.gitattributes);
	// the merge is one of the enforcement points.
	var dstRaw []byte
	if b, err := os.ReadFile(dstPath); err == nil {
		dstRaw = normalizeToLF(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read dst: %w", err)
	}
	dstLines := []string{}
	if len(dstRaw) > 0 {
		// Strip the file's trailing LF before splitting so a
		// well-formed (LF-terminated) input doesn't produce a stray
		// empty trailing element that would re-emit as a blank
		// line on every merge.
		dstLines = strings.Split(strings.TrimRight(string(dstRaw), "\n"), "\n")
	}

	applied := map[string]bool{}
	for i, line := range dstLines {
		if m := envLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if v, ok := srcMap[m[1]]; ok {
				dstLines[i] = m[1] + "=" + v
				applied[m[1]] = true
			}
		}
	}
	// Append any src keys not yet in dst, in their source order so
	// repeated merges yield a stable diff (alphabetical sort would
	// shuffle existing files on first merge). The optional header is
	// inserted exactly once -- guarded by a sentinel so the second
	// appended key in this run doesn't trigger a second header.
	headerEmitted := false
	headerNeeded := header != ""
	if headerNeeded {
		// Idempotent check: if dst already carries this exact header
		// line (from a prior merge), skip emitting it again.
		for _, line := range dstLines {
			if strings.TrimSpace(line) == strings.TrimSpace(header) {
				headerNeeded = false
				break
			}
		}
	}
	for _, k := range srcOrder {
		if applied[k] {
			continue
		}
		if headerNeeded && !headerEmitted {
			dstLines = append(dstLines, header)
			headerEmitted = true
		}
		dstLines = append(dstLines, k+"="+srcMap[k])
		applied[k] = true
	}

	body := strings.Join(dstLines, "\n") + "\n"
	if err := atomicWrite(dstPath, []byte(body)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	emit(stdout, "xplat env-merge:", len(srcOrder), "key(s) merged into", dstPath)
	return nil
}

// readEnvFile parses a .env file into a key->value map plus a slice
// of keys in source order. Comments (#) and blank lines are skipped.
// Whitespace around the '=' is tolerated; the value is preserved
// exactly as written after the first '='. A malformed line (no '=' or
// invalid key) is silently skipped -- same lenient behavior the
// docker-compose env-file parser uses.
func readEnvFile(path string) (map[string]string, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// Funnel through normalizeToLF so a CRLF src cannot leak \r into
	// the parsed VALUE -- if it did, the \r would write back into dst
	// (the regex captures everything after '=' through end-of-line)
	// and re-introduce CR bytes into the LF dst. Belt-and-braces
	// alongside envMerge's own dst normalization.
	b = normalizeToLF(b)
	out := map[string]string{}
	order := []string{}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := envLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, dup := out[m[1]]; !dup {
			order = append(order, m[1])
		}
		out[m[1]] = m[2]
	}
	return out, order, nil
}

// copyFile copies src to dst, creating dst's parent directory if needed and
// marking dst executable (0o755) so POSIX git hooks are runnable; on Windows
// the mode is largely cosmetic but harmless. The mode is set after the copy
// too: OpenFile applies it only when it creates dst, and git silently skips
// a hook left non-executable (an existing 0644 .git/hooks/pre-commit).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// targetRe matches a documented target line: "<name>: ... ## <description>".
// Name excludes '.' so .PHONY/.SUFFIXES lines are skipped, matching the
// historical awk pattern ^[a-zA-Z_-]+ (digits added for targets like
// build-core / migrate-staged).
var targetRe = regexp.MustCompile(`^([A-Za-z0-9_-]+):.*?##\s+(.+)$`)

// printHelp renders the make help screen from the given makefile(s),
// replacing the awk (Unix) / powershell (Windows) HELP_CMD branch that
// broke when $(OS)=Windows_NT picked the powershell form but recipes ran
// under sh. "##@ group" lines become section headers; documented targets
// are listed aligned. Plain text (no ANSI) for terminal-agnostic output.
func printHelp(stdout, stderr io.Writer, files []string) int {
	for _, mf := range files {
		b, err := os.ReadFile(mf)
		if err != nil {
			emit(stderr, "xplat help:", err)
			return 1
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			line = strings.TrimRight(line, "\r")
			if g, ok := strings.CutPrefix(line, "##@ "); ok {
				emit(stdout, "")
				emit(stdout, strings.TrimSpace(g))
				continue
			}
			if m := targetRe.FindStringSubmatch(line); m != nil {
				emit(stdout, fmt.Sprintf("  %-28s %s", m[1], strings.TrimSpace(m[2])))
			}
		}
	}
	return 0
}
