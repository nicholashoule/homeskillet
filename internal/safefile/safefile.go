// Package safefile reads and writes the small files homeskillet keeps: the
// manifest it reads, and the records it writes beside installed binaries and
// in a home directory's state directory.
//
// Reads are bounded, so a file swapped for something huge, or for a device,
// fails instead of exhausting memory. Writes are atomic, so a crash or a full
// disk never leaves a partial file where a complete one was expected.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// ReadAll reads all of r, failing if it holds more than limit bytes.
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

// Read reads the file at path, failing if it holds more than limit bytes.
func Read(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // G304: callers pass paths they have checked
	if err != nil {
		return nil, err
	}
	data, err := ReadAll(file, limit)
	if err := errors.Join(err, file.Close()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}

// Write creates or replaces path with what write produces, so that path never
// holds a partial file, even after a crash. It writes under a temporary name
// in the same directory, flushes the data to disk, renames it into place, and
// flushes the directory so the rename itself survives a crash. Missing
// directories are created private (0700), as everywhere in homeskillet.
//
// os.CreateTemp opens with O_EXCL, so the temporary file can be neither an
// existing file nor a symlink planted to redirect the write. Its name starts
// with TempPrefix, so callers can recognize leftovers from an interrupted
// write.
func Write(path string, mode os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, TempPrefix+"*")
	if err != nil {
		return err
	}
	err = write(temporary)
	err = errors.Join(err, temporary.Chmod(mode), temporary.Sync(), temporary.Close())
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(temporary.Name()))
	}
	return SyncDir(dir)
}

// TempPrefix starts the name of every file Write has not finished.
const TempPrefix = ".homeskillet-"

// SyncDir flushes dir's entries to disk, so a file renamed, linked, or
// removed there stays that way after a crash. On Windows it does nothing:
// flushing needs write access, which a directory handle cannot have there, so
// Sync would fail with access denied; NTFS journals the rename instead, so it
// is applied whole or not at all.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir) //nolint:gosec // G304: callers pass directories they have checked
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
