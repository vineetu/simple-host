package storage

import "golang.org/x/sys/unix"

// O_PATH needs search permission, rather than directory-listing permission.
const searchDirectoryFlags = unix.O_PATH
