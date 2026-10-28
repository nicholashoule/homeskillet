//go:build linux || darwin || windows

// Package mirror copies a configuration repository from Windows into a WSL
// distribution, where homeskillet links from the copy. WSL cannot link from
// the Windows clone itself: Windows drives under /mnt show every file as
// writable by everyone, so the Linux build refuses them. So Windows checks
// the clone, as only its ownership rules can, and sends what it checked;
// the Linux build receives it into a private directory of its own, replacing
// the copy before it whole, or compares it with that copy and changes
// nothing.
//
// The stream is a line of text before each directory and file, and each
// file's bytes after its line:
//
//	homeskillet mirror 1
//	d <path>
//	f <x or -> <size> <path>
//	<size bytes>
//	end
//
// Paths are relative, separated by slashes, and printable; x marks a file Git
// records as executable.
package mirror

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// header starts every stream, naming the format and its version, so that a
// build that reads another format refuses it rather than misreading it.
const header = "homeskillet mirror 1"

// Limits on a stream, which bound what a copy can take of the disk. A
// repository of dotfiles is far smaller; one beyond these holds something
// that does not belong in a home, or the stream is not a mirror at all.
const (
	maxFile    = 128 << 20
	maxTotal   = 512 << 20
	maxEntries = 100_000
	maxLine    = 8 << 10
)

// Send writes the repository at repo, which must be canonical and pass
// trust.CheckRepository, to w as a stream: every directory and regular file
// in it, except .git, each file only after trust.Resolve has checked it and
// the directories on its way, as package link checks a source. A symlink or
// any other kind of file is an error, since the copy holds only what was
// checked. Files Git records as executable are marked so.
func Send(ctx context.Context, w io.Writer, repo string) error {
	executable := executables(ctx, repo)
	out := bufio.NewWriter(w)
	if _, err := out.WriteString(header + "\n"); err != nil {
		return err
	}
	var entries, total int64
	err := filepath.WalkDir(repo, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == repo {
			return nil
		}
		rel, err := filepath.Rel(repo, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(d.Name(), ".git") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !config.Printable(rel) || strings.ContainsAny(rel, "\n\\") {
			return fmt.Errorf("%q: names must not contain control or formatting characters", rel)
		}
		if entries++; entries > maxEntries {
			return fmt.Errorf("%s holds more than %d files and directories, more than a copy takes", repo, maxEntries)
		}
		switch {
		case d.IsDir():
			_, err = fmt.Fprintf(out, "d %s\n", rel)
			return err
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is a symlink or another special file; the copy in WSL holds only files and folders", name)
		}
		size, err := sendFile(out, repo, name, rel, executable[rel])
		total += size
		if err == nil && total > maxTotal {
			err = fmt.Errorf("%s holds more than %d MiB, more than a copy takes", repo, maxTotal>>20)
		}
		return err
	})
	if err != nil {
		return err
	}
	if _, err := out.WriteString("end\n"); err != nil {
		return err
	}
	return out.Flush()
}

// sendFile checks the file at name and writes its line and bytes to out,
// returning its size.
func sendFile(out io.Writer, repo, name, rel string, executable bool) (int64, error) {
	resolved, err := trust.Resolve(repo, name)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(resolved) //nolint:gosec // G304: a file in the repository, checked by trust.Resolve just before
	if err != nil {
		return 0, err
	}
	defer file.Close() //nolint:errcheck // opened only to read; closing it cannot lose anything
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > maxFile {
		return 0, fmt.Errorf("%s is larger than %d MiB, more than a copy takes", name, maxFile>>20)
	}
	mark := "-"
	if executable {
		mark = "x"
	}
	if _, err := fmt.Fprintf(out, "f %s %d %s\n", mark, info.Size(), rel); err != nil {
		return 0, err
	}
	// A file that shrinks while it is read would leave the stream short; one
	// that grows is sent as it was when its size was read.
	if _, err := io.CopyN(out, file, info.Size()); err != nil {
		return 0, fmt.Errorf("%s changed while it was copied: %w", name, err)
	}
	return info.Size(), nil
}

// executables returns the files Git records as executable in repo's index,
// by slash-separated path, or none when Git is missing or repo is not a work
// tree. Windows keeps no executable bit, so Git's record is the one place it
// survives.
func executables(ctx context.Context, repo string) map[string]bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, git, "-C", repo, "ls-files", "--stage", "-z") //nolint:gosec // G204: Git found on PATH, reading the index of the repository just checked
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	found := make(map[string]bool)
	for record := range bytes.SplitSeq(output, []byte{0}) {
		mode, rest, _ := bytes.Cut(record, []byte{' '})
		_, name, ok := bytes.Cut(rest, []byte{'\t'})
		if ok && string(mode) == "100755" {
			found[string(name)] = true
		}
	}
	return found
}

// entry is one directory or file in a stream.
type entry struct {
	dir        bool
	path       string
	executable bool
	size       int64
}

// reader reads a stream, checking every line before anything acts on it.
type reader struct {
	in      *bufio.Reader
	entries int
	total   int64
}

func newReader(r io.Reader) (*reader, error) {
	rd := &reader{in: bufio.NewReaderSize(r, maxLine)}
	line, err := rd.line()
	if err != nil {
		return nil, err
	}
	if line != header {
		return nil, errors.New("not a homeskillet mirror of this version; install the Linux build that matches Windows's")
	}
	return rd, nil
}

// line returns the next line without its newline. A stream that ends before
// "end" is an error, so a broken transfer never counts as a whole copy.
func (rd *reader) line() (string, error) {
	line, err := rd.in.ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return "", errors.New("mirror: a line is too long")
	case errors.Is(err, io.EOF):
		return "", errors.New("mirror: the stream ended before its end; nothing was changed")
	case err != nil:
		return "", err
	}
	return string(line[:len(line)-1]), nil
}

// next returns the next entry, or false after the last.
func (rd *reader) next() (entry, bool, error) {
	line, err := rd.line()
	if err != nil {
		return entry{}, false, err
	}
	if line == "end" {
		if _, err := rd.in.ReadByte(); !errors.Is(err, io.EOF) {
			return entry{}, false, errors.New("mirror: data after the end")
		}
		return entry{}, false, nil
	}
	if rd.entries++; rd.entries > maxEntries {
		return entry{}, false, errors.New("mirror: too many files and directories")
	}
	var e entry
	switch kind, rest, _ := strings.Cut(line, " "); kind {
	case "d":
		e = entry{dir: true, path: rest}
	case "f":
		fields := strings.SplitN(rest, " ", 3)
		if len(fields) != 3 || fields[0] != "x" && fields[0] != "-" {
			return entry{}, false, fmt.Errorf("mirror: malformed line %q", line)
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || size < 0 || size > maxFile {
			return entry{}, false, fmt.Errorf("mirror: bad size in %q", line)
		}
		if rd.total += size; rd.total > maxTotal {
			return entry{}, false, errors.New("mirror: more data than a copy takes")
		}
		e = entry{path: fields[2], executable: fields[0] == "x", size: size}
	default:
		return entry{}, false, fmt.Errorf("mirror: malformed line %q", line)
	}
	if !validPath(e.path) {
		return entry{}, false, fmt.Errorf("mirror: %q is not a relative path inside the copy", e.path)
	}
	return e, true, nil
}

// validPath reports whether p is a clean, printable, relative path that stays
// inside the directory it is joined to.
func validPath(p string) bool {
	return p != "" && config.Printable(p) && !strings.Contains(p, `\`) && path.Clean(p) == p &&
		filepath.IsLocal(filepath.FromSlash(p))
}

// mode returns the permissions a copied file or directory gets: private, as
// every directory homeskillet creates is, and executable where Git said so.
func (e entry) mode() os.FileMode {
	if e.dir || e.executable {
		return 0o700
	}
	return 0o600
}
