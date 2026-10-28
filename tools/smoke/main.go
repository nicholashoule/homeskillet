// Command smoke builds homeskillet and uses it as a person would, for make
// smoke and CI on Linux, macOS, and Windows. It links the example repository
// for the system it runs on, from examples/, into a scratch home, checks the
// links on disk after each command, and unlinks them; it also forces a link
// past a conflict and purges the backup that makes.
//
// Unit tests run the same code in-process. This runs the built binary, so it
// also covers what only a real process has: arguments, exit statuses, the
// privilege check, and symlinks made by the program a user installs.
//
// Everything happens in one temporary directory, removed afterward: the
// binary, a copy of the example, the home, and the data directory. Every
// command that touches a home gets --home and --data-dir, so the real home is
// never read or changed.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// examples maps an operating system to its example repository in examples/.
var examples = map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}

// conflict is what the smoke test writes where a link should go, to check
// that link refuses to replace it and link --force keeps it as a backup.
const conflict = "a file homeskillet did not create\n"

func main() {
	allowRoot := flag.Bool("allow-root", false, "pass --allow-root, for an elevated Windows terminal, as on GitHub's Windows runners; root on Linux and macOS is detected")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/smoke [-allow-root]")
		os.Exit(2)
	}
	// Geteuid is -1 on Windows, where elevation takes the flag.
	if err := run(context.Background(), os.Stdout, *allowRoot || os.Geteuid() == 0); err != nil {
		fmt.Fprintf(os.Stderr, "smoke: %v\n", err)
		os.Exit(1)
	}
}

// smoke holds the paths of one run.
type smoke struct {
	out       io.Writer
	bin       string // the built homeskillet
	repo      string // the copy of the example
	home      string // the scratch home
	data      string // the scratch data directory
	allowRoot bool
	files     []string // targets the example links, relative to home
}

// run builds homeskillet, copies the example for this system, and runs every
// check, removing the temporary directory whatever happens.
func run(ctx context.Context, out io.Writer, allowRoot bool) (err error) {
	example, ok := examples[runtime.GOOS]
	if !ok {
		return fmt.Errorf("examples/ has no repository for %s", runtime.GOOS)
	}
	root, err := moduleRoot(ctx)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "homeskillet-smoke-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tmp)) }()
	// Paths are compared with what homeskillet reports, which resolves them:
	// the temporary directory can be reached through a symlink, as on macOS,
	// or a short name, as on GitHub's Windows runners.
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return err
	}

	s := &smoke{
		out:       out,
		bin:       filepath.Join(tmp, "bin", "homeskillet"),
		repo:      filepath.Join(tmp, "repo"),
		home:      filepath.Join(tmp, "home"),
		data:      filepath.Join(tmp, "data"),
		allowRoot: allowRoot,
	}
	if runtime.GOOS == "windows" {
		s.bin += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", s.bin, ".") //nolint:gosec // G204: builds into the temporary directory this command made
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, output)
	}
	s.ok("built %s", s.bin)

	for _, dir := range []string{s.repo, s.home} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(root, "examples", example), s.repo); err != nil {
		return fmt.Errorf("copying examples/%s: %w", example, err)
	}
	if s.files, err = files(filepath.Join(s.repo, "home")); err != nil {
		return err
	}
	if len(s.files) == 0 {
		return fmt.Errorf("examples/%s/home holds no files to link", example)
	}
	s.ok("copied examples/%s: %d files under home/", example, len(s.files))

	for _, check := range []func(context.Context) error{
		s.version, s.check, s.dryRun, s.link, s.unlink, s.force, s.setup, s.purge,
	} {
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}

// version checks the binary runs at all. version takes no options, not even
// --allow-root, as it changes nothing and runs as anyone.
func (s *smoke) version(ctx context.Context) error {
	output, err := s.exec(ctx, "version")
	if err != nil || !strings.HasPrefix(output, "homeskillet ") {
		return failed("version", output, err)
	}
	s.ok("version: %s", strings.SplitN(output, "\n", 2)[0])
	return nil
}

// check checks the example is a valid repository.
func (s *smoke) check(ctx context.Context) error {
	output, err := s.run(ctx, "check", s.repo)
	if err != nil || !strings.HasPrefix(output, "valid\t") {
		return failed("check", output, err)
	}
	s.ok("check: valid")
	return nil
}

// dryRun checks link --dry-run changes nothing.
func (s *smoke) dryRun(ctx context.Context) error {
	return s.unchanged("link --dry-run", func() error {
		output, err := s.inHome(ctx, "link", s.repo, "--dry-run")
		if err != nil {
			return failed("link --dry-run", output, err)
		}
		return nil
	})
}

// link checks link makes every link, status reports them, and linking again
// changes nothing.
func (s *smoke) link(ctx context.Context) error {
	if output, err := s.inHome(ctx, "link", s.repo); err != nil {
		return failed("link", output, err)
	}
	if err := s.linked(); err != nil {
		return fmt.Errorf("after link: %w", err)
	}
	s.ok("link: %d links, each to its source", len(s.files))

	output, err := s.inHome(ctx, "status", s.repo)
	if err != nil {
		return failed("status", output, err)
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != len(s.files) || slices.ContainsFunc(lines, func(line string) bool { return !strings.HasPrefix(line, "linked\t") }) {
		return failed("status, expecting a linked line for each file", output, nil)
	}
	s.ok("status: every file linked")

	return s.unchanged("link again", func() error {
		output, err := s.inHome(ctx, "link", s.repo)
		if err != nil {
			return failed("link again", output, err)
		}
		return nil
	})
}

// unlink checks unlink removes every link.
func (s *smoke) unlink(ctx context.Context) error {
	if output, err := s.inHome(ctx, "unlink", s.repo); err != nil {
		return failed("unlink", output, err)
	}
	if err := s.noLinks(); err != nil {
		return fmt.Errorf("after unlink: %w", err)
	}
	s.ok("unlink: no links left")
	return nil
}

// force checks link refuses a file it did not create and changes nothing,
// then link --force moves it into a backup and links, and backups lists it.
func (s *smoke) force(ctx context.Context) error {
	target := filepath.Join(s.home, filepath.FromSlash(s.files[0]))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(conflict), 0o600); err != nil {
		return err
	}
	if err := s.unchanged("link over a conflict", func() error {
		output, err := s.inHome(ctx, "link", s.repo)
		if err == nil {
			return failed("link over a conflict, expecting a refusal", output, nil)
		}
		return nil
	}); err != nil {
		return err
	}

	if output, err := s.inHome(ctx, "link", s.repo, "--force"); err != nil {
		return failed("link --force", output, err)
	}
	if err := s.linked(); err != nil {
		return fmt.Errorf("after link --force: %w", err)
	}
	backup, err := s.backup(ctx, s.files[0])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(backup) //nolint:gosec // G304: the backup homeskillet listed, in the scratch data directory
	if err != nil {
		return fmt.Errorf("reading the backup: %w", err)
	}
	if string(data) != conflict {
		return fmt.Errorf("the backup %s holds %q, not the file it replaced", backup, data)
	}
	s.ok("link --force: moved %s into a backup, and linked", s.files[0])
	return s.unlink(ctx)
}

// backup returns the one backup backups lists, after checking it came from
// file, a target relative to the home.
func (s *smoke) backup(ctx context.Context, file string) (string, error) {
	output, err := s.inHome(ctx, "backups")
	if err != nil {
		return "", failed("backups", output, err)
	}
	// backup, its path, where it came from in the home, when, and its size.
	fields := strings.Split(strings.TrimSuffix(output, "\n"), "\t")
	if len(fields) != 5 || fields[0] != "backup" || !samePath(fields[2], filepath.FromSlash(file)) {
		return "", failed("backups, expecting one backup of "+file, output, nil)
	}
	return fields[1], nil
}

// setup checks setup lists the example's steps; a dry run runs none.
func (s *smoke) setup(ctx context.Context) error {
	output, err := s.inHome(ctx, "setup", s.repo, "--dry-run")
	if err != nil {
		return failed("setup --dry-run", output, err)
	}
	s.ok("setup --dry-run: listed the steps")
	return nil
}

// purge checks backups purge --yes deletes the backup link --force made.
func (s *smoke) purge(ctx context.Context) error {
	if output, err := s.inHome(ctx, "backups", "purge", "--yes"); err != nil {
		return failed("backups purge --yes", output, err)
	}
	output, err := s.inHome(ctx, "backups")
	if err != nil || strings.TrimSpace(output) != "" {
		return failed("backups after a purge, expecting none", output, err)
	}
	s.ok("backups purge --yes: no backups left")
	return nil
}

// linked checks every file of the example has a link in home that resolves
// to its source in the repository.
func (s *smoke) linked() error {
	for _, file := range s.files {
		target := filepath.Join(s.home, filepath.FromSlash(file))
		source := filepath.Join(s.repo, "home", filepath.FromSlash(file))
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("%s is not a symlink", target)
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return err
		}
		if !samePath(resolved, source) {
			return fmt.Errorf("%s leads to %s, not %s", target, resolved, source)
		}
	}
	return nil
}

// noLinks checks home holds no symlink at all.
func (s *smoke) noLinks() error {
	entries, err := tree(s.home)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry, " symlink") {
			return fmt.Errorf("%s is still there", entry)
		}
	}
	return nil
}

// unchanged runs do and checks the home, with the data directory, looks
// exactly as it did before.
func (s *smoke) unchanged(name string, do func() error) error {
	before, err := s.state()
	if err != nil {
		return err
	}
	if err := do(); err != nil {
		return err
	}
	after, err := s.state()
	if err != nil {
		return err
	}
	if !slices.Equal(before, after) {
		return fmt.Errorf("%s changed the home or data directory:\nbefore %q\nafter  %q", name, before, after)
	}
	s.ok("%s: nothing changed", name)
	return nil
}

// state lists the home and the data directory, when there is one.
func (s *smoke) state() ([]string, error) {
	home, err := tree(s.home)
	if err != nil {
		return nil, err
	}
	data, err := tree(s.data)
	if errors.Is(err, fs.ErrNotExist) {
		return home, nil
	}
	return append(home, data...), err
}

// run runs the built homeskillet with args, and --allow-root when the runner
// needs it, and returns what it printed.
func (s *smoke) run(ctx context.Context, args ...string) (string, error) {
	if s.allowRoot {
		args = append(args, "--allow-root")
	}
	return s.exec(ctx, args...)
}

// exec runs the built homeskillet with exactly args, and returns what it
// printed. Its output is not a terminal, so homeskillet prints one
// tab-separated line per file, a stable format.
func (s *smoke) exec(ctx context.Context, args ...string) (string, error) {
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, s.bin, args...) //nolint:gosec // G204: runs the homeskillet this command built, with arguments it chose
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return output.String(), err
}

// inHome runs a command that acts on a home, in the scratch home and data
// directory.
func (s *smoke) inHome(ctx context.Context, args ...string) (string, error) {
	return s.run(ctx, append(args, "--home", s.home, "--data-dir", s.data)...)
}

// ok reports a check that passed.
func (s *smoke) ok(format string, args ...any) {
	_, _ = fmt.Fprintf(s.out, "ok  "+format+"\n", args...)
}

// failed describes a command that did not do what was expected.
func failed(command, output string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", command, err, output)
	}
	return fmt.Errorf("%s: unexpected output:\n%s", command, output)
}

// moduleRoot returns the directory holding go.mod, so the command runs from
// anywhere in the repository.
func moduleRoot(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(output))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("run it inside the homeskillet module")
	}
	return filepath.Dir(gomod), nil
}

// files lists the regular files under dir, relative to it, with '/'.
func files(dir string) ([]string, error) {
	var found []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			found = append(found, path)
		}
		return err
	})
	return found, err
}

// tree lists everything under dir, relative to it, each with its type.
func tree(dir string) ([]string, error) {
	var found []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		kind := "file"
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			kind = "symlink"
		case entry.IsDir():
			kind = "dir"
		}
		found = append(found, path+" "+kind)
		return nil
	})
	return found, err
}

// copyTree copies the files under from into to, which exists, with modes the
// ownership rules accept whatever the umask.
func copyTree(from, to string) (err error) {
	root, err := os.OpenRoot(to)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	source := os.DirFS(from)
	return fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == "." {
			return err
		}
		if entry.IsDir() {
			return root.Mkdir(filepath.FromSlash(path), 0o700)
		}
		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		file, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		return errors.Join(err, file.Close())
	})
}

// samePath reports whether two cleaned paths name the same file, ignoring
// case on Windows, whose file systems usually do.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
