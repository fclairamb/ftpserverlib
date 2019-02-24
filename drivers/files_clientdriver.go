package drivers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/karrick/godirwalk"
	"github.com/r0123r/ftpserver/server"
)

// ClientDriver defines a very basic client driver
type ClientDriver struct {
	BaseDir string // Base directory from which to server file
}

// ChangeDirectory changes the current working directory
func (driver *ClientDriver) ChangeDirectory(cc server.ClientContext, directory string) error {
	if directory == "/debug" {
		cc.SetDebug(!cc.Debug())
		return nil
	} else if directory == "/virtual" {
		return nil
	}
	_, err := os.Stat(driver.BaseDir + directory)
	return err
}

// MakeDirectory creates a directory
func (driver *ClientDriver) MakeDirectory(cc server.ClientContext, directory string) error {
	return os.Mkdir(driver.BaseDir+directory, 0777)
}

func (driver *ClientDriver) AsyncListFiles(cc server.ClientContext, cfiles chan<- os.FileInfo) {
	defer func() {
		close(cfiles)
	}()

	if cc.Path() == "/virtual" {
		cfiles <- &VirtualFileInfo{
			name: "localpath.txt",
			size: 1024,
		}
		cfiles <- &VirtualFileInfo{
			name: "file2.txt",
			size: 2048,
		}
		return
	} else if cc.Path() == "/debug" {
		return
	}
	// We add a virtual dir
	if cc.Path() == "/" {
		cfiles <- &VirtualFileInfo{
			name:  "virtual",
			isDir: true,
			size:  4096,
		}
	}
	path := filepath.Join(driver.BaseDir, cc.Path())

	//files, err := ioutil.ReadDir(path)
	godirwalk.Walk(path, &godirwalk.Options{
		FollowSymbolicLinks: true,
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			if de.IsDir() && osPathname == path {
				return nil
			}
			if !de.IsSymlink() {
				ff, err := os.Stat(osPathname)
				if err == nil {
					cfiles <- ff
				}
			} else {
				cfiles <- &VirtualFileInfo{
					name:  de.Name(),
					isDir: true,
				}
			}
			return fmt.Errorf("stop")
		},
		ErrorCallback: func(osPathname string, err error) godirwalk.ErrorAction {
			return godirwalk.SkipNode
		},
		Unsorted: true, // set true for faster yet non-deterministic enumeration (see godoc)
	})

}

// ListFiles lists the files of a directory
func (driver *ClientDriver) ListFiles(cc server.ClientContext) ([]os.FileInfo, error) {

	if cc.Path() == "/virtual" {
		files := make([]os.FileInfo, 0)
		files = append(files,
			&VirtualFileInfo{
				name: "localpath.txt",
				size: 1024,
			},
			&VirtualFileInfo{
				name: "file2.txt",
				size: 2048,
			},
		)
		return files, nil
	} else if cc.Path() == "/debug" {
		return make([]os.FileInfo, 0), nil
	}

	path := driver.BaseDir + cc.Path()

	files, err := ioutil.ReadDir(path)

	// We add a virtual dir
	if cc.Path() == "/" && err == nil {
		files = append(files, &VirtualFileInfo{
			name:  "virtual",
			isDir: true,
			size:  4096,
		})
	}

	return files, err
}

// OpenFile opens a file in 3 possible modes: read, write, appending write (use appropriate flags)
func (driver *ClientDriver) OpenFile(cc server.ClientContext, path string, flag int) (server.FileStream, error) {

	if path == "/virtual/localpath.txt" {
		return &VirtualFile{content: []byte(driver.BaseDir)}, nil
	}

	path = driver.BaseDir + path

	// If we are writing and we are not in append mode, we should remove the file
	if (flag & os.O_WRONLY) != 0 {
		flag |= os.O_CREATE
		if (flag & os.O_APPEND) == 0 {
			os.Remove(path)
		}
	}

	return os.OpenFile(path, flag, 0666)
}

// GetFileInfo gets some info around a file or a directory
func (driver *ClientDriver) GetFileInfo(cc server.ClientContext, path string) (os.FileInfo, error) {
	switch path {
	case "/virtual":
		return &VirtualFileInfo{name: "virtual", size: 4096, isDir: true}, nil
	case "/debug":
		return &VirtualFileInfo{name: "debug", size: 4096, isDir: true}, nil
	}

	path = driver.BaseDir + path

	return os.Stat(path)
}

// CanAllocate gives the approval to allocate some data
func (driver *ClientDriver) CanAllocate(cc server.ClientContext, size int) (bool, error) {
	return true, nil
}

// ChmodFile changes the attributes of the file
func (driver *ClientDriver) ChmodFile(cc server.ClientContext, path string, mode os.FileMode) error {
	path = driver.BaseDir + path

	return os.Chmod(path, mode)
}

// DeleteFile deletes a file or a directory
func (driver *ClientDriver) DeleteFile(cc server.ClientContext, path string) error {
	path = driver.BaseDir + path

	return os.Remove(path)
}

// RenameFile renames a file or a directory
func (driver *ClientDriver) RenameFile(cc server.ClientContext, from, to string) error {
	from = driver.BaseDir + from
	to = driver.BaseDir + to

	return os.Rename(from, to)
}
