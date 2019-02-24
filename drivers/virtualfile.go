package drivers

import (
	"io"
	"os"
	"time"
)

// The virtual file is an example of how you can implement a purely virtual file
type VirtualFile struct {
	content    []byte // Content of the file
	readOffset int    // Reading offset
}

func (f *VirtualFile) Close() error {
	return nil
}

func (f *VirtualFile) Read(buffer []byte) (int, error) {
	n := copy(buffer, f.content[f.readOffset:])
	f.readOffset += n
	if n == 0 {
		return 0, io.EOF
	}

	return n, nil
}

func (f *VirtualFile) Seek(n int64, w int) (int64, error) {
	return 0, nil
}

func (f *VirtualFile) Write(buffer []byte) (int, error) {
	return 0, nil
}

type VirtualFileInfo struct {
	name    string
	isDir   bool
	modTime time.Time
	size    int64
}

func (f *VirtualFileInfo) Name() string {
	return f.name
}

func (f *VirtualFileInfo) Size() int64 {
	return f.size
}

func (f *VirtualFileInfo) Mode() os.FileMode {
	if f.isDir {
		return os.ModeDir | os.ModePerm
	}
	return os.ModePerm
}

func (f *VirtualFileInfo) ModTime() time.Time {
	if f.modTime.IsZero() {
		return time.Now().UTC()
	}
	return f.modTime
}

func (f *VirtualFileInfo) IsDir() bool {
	return f.isDir
}

func (f *VirtualFileInfo) Sys() interface{} {
	return nil
}
