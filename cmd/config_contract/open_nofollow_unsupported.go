//go:build !darwin && !linux

package main

import (
	"fmt"
	"os"
)

func openStagedFileNoFollow(string) (*os.File, error) {
	return nil, fmt.Errorf("staged config inspection requires final-symlink-safe open support")
}
