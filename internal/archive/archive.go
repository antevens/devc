// Package archive builds tar archives in memory, to copy files into
// containers.
package archive

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Archive is a tar archive built in memory. Errors are kept and returned by
// Bytes.
type Archive struct {
	buf      bytes.Buffer
	tw       *tar.Writer
	uid, gid int
	err      error
}

// New returns an archive whose entries are owned by uid and gid. The owners
// only apply when the archive is extracted as root.
func New(uid, gid int) *Archive {
	a := &Archive{uid: uid, gid: gid}
	a.tw = tar.NewWriter(&a.buf)
	return a
}

// Entry returns the entry name of an absolute path, for archives extracted
// at the root directory.
func Entry(absPath string) string {
	return strings.TrimPrefix(absPath, "/")
}

// AddDir adds a directory entry.
func (a *Archive) AddDir(name string, mode fs.FileMode) {
	a.write(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: int64(mode.Perm()), ModTime: time.Now()}, nil)
}

// AddFile adds a file with the given contents.
func (a *Archive) AddFile(name string, data []byte, mode fs.FileMode) {
	a.write(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: int64(mode.Perm()), Size: int64(len(data)), ModTime: time.Now()}, data)
}

// AddTree adds the file or directory src as name, following symlinks. A
// missing src is ignored, as are unreadable files, broken symlinks and
// special files. skip lists top-level entries of src that are left out.
func (a *Archive) AddTree(name, src string, skip map[string]bool) error {
	return a.addPath(name, src, skip, map[string]bool{})
}

// Bytes finishes the archive and returns its contents.
func (a *Archive) Bytes() ([]byte, error) {
	if a.err != nil {
		return nil, fmt.Errorf("build archive: %w", a.err)
	}
	if err := a.tw.Close(); err != nil {
		return nil, fmt.Errorf("build archive: %w", err)
	}
	return a.buf.Bytes(), nil
}

func (a *Archive) write(h *tar.Header, data []byte) {
	if a.err != nil {
		return
	}
	h.Uid, h.Gid = a.uid, a.gid
	if a.err = a.tw.WriteHeader(h); a.err == nil && data != nil {
		_, a.err = a.tw.Write(data)
	}
}

// addPath adds src as name. ancestors holds the real paths of the
// directories being added, to stop at symlink loops.
func (a *Archive) addPath(name, src string, skip, ancestors map[string]bool) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) || os.IsPermission(err) {
		return nil
	}
	if err != nil {
		return err
	}

	switch {
	case info.Mode().IsRegular():
		data, err := os.ReadFile(src)
		if os.IsPermission(err) {
			return nil
		}
		if err != nil {
			return err
		}
		a.write(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: int64(info.Mode().Perm()), Size: int64(len(data)), ModTime: info.ModTime()}, data)
	case info.IsDir():
		real, err := filepath.EvalSymlinks(src)
		if err != nil {
			return err
		}
		if ancestors[real] {
			return nil
		}
		ancestors[real] = true
		defer delete(ancestors, real)

		entries, err := os.ReadDir(src)
		if os.IsPermission(err) {
			return nil
		}
		if err != nil {
			return err
		}
		a.write(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}, nil)
		for _, e := range entries {
			if skip[e.Name()] {
				continue
			}
			if err := a.addPath(path.Join(name, e.Name()), filepath.Join(src, e.Name()), nil, ancestors); err != nil {
				return err
			}
		}
	}
	return a.err
}
