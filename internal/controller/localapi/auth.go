package localapi

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	AuthFilename   = "controller.auth"
	sessionKeySize = 32
)

func createSessionSecret(stateDir string) (string, error) {
	raw := make([]byte, sessionKeySize)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate controller session secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	path := filepath.Join(stateDir, AuthFilename)

	tmp, err := os.CreateTemp(stateDir, ".controller-auth-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create controller auth temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("secure controller auth temp file: %w", err)
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write controller auth temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync controller auth temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close controller auth temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("activate controller session secret: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure controller auth file: %w", err)
	}
	return secret, nil
}

func loadSessionSecret(stateDir string) (string, error) {
	path := filepath.Join(stateDir, AuthFilename)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect controller auth file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("controller auth path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("controller auth file permissions are too broad: %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read controller auth file: %w", err)
	}
	secret := strings.TrimSpace(string(data))
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(raw) != sessionKeySize {
		return "", fmt.Errorf("controller auth file is invalid")
	}
	return secret, nil
}

func removeSessionSecret(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, AuthFilename))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
