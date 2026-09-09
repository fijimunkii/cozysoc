//go:build darwin || linux

package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const maxFilesystemBytes = uint64(1<<63 - 1)

func readFilesystemCapacity(path string) (FilesystemCapacity, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return FilesystemCapacity{}, fmt.Errorf("read filesystem capacity: %w", err)
	}
	blockSize := uint64(stat.Bsize)
	if blockSize == 0 {
		return FilesystemCapacity{}, fmt.Errorf("filesystem block size is zero")
	}
	total, err := filesystemBytes(uint64(stat.Blocks), blockSize)
	if err != nil {
		return FilesystemCapacity{}, err
	}
	available, err := filesystemBytes(uint64(stat.Bavail), blockSize)
	if err != nil {
		return FilesystemCapacity{}, err
	}
	return classifyFilesystemCapacity(true, total, available), nil
}

func filesystemBytes(blocks, blockSize uint64) (int64, error) {
	if blockSize == 0 || blocks > maxFilesystemBytes/blockSize {
		return 0, fmt.Errorf("filesystem capacity exceeds supported range")
	}
	return int64(blocks * blockSize), nil
}
