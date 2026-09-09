//go:build !darwin && !linux

package storage

func readFilesystemCapacity(string) (FilesystemCapacity, error) {
	return classifyFilesystemCapacity(false, 0, 0), nil
}
