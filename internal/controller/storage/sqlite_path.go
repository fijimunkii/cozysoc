package storage

import (
	"net/url"
	"path/filepath"
)

// sqliteFileURI treats path only as a filesystem path. Resolve relative paths
// before encoding so even a leading "file:" is literal. Query parameters belong
// to the connection owner, never to the state-directory input.
func sqliteFileURI(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: absolute}).String(), nil
}
