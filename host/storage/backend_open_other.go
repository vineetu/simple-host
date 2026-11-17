//go:build !linux && !darwin

package storage

import "golang.org/x/sys/unix"

const searchDirectoryFlags = unix.O_RDONLY
