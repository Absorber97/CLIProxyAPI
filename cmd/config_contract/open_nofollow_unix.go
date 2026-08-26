//go:build darwin || linux

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openStagedFileNoFollow(path string) (*os.File, error) {
	fd, errOpen := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errOpen != nil {
		return nil, errOpen
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		if errClose := unix.Close(fd); errClose != nil {
			return nil, fmt.Errorf("create staged config file handle and close descriptor")
		}
		return nil, fmt.Errorf("create staged config file handle")
	}
	return file, nil
}
