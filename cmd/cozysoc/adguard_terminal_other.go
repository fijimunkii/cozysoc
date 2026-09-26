//go:build !darwin

package main

import (
	"errors"
	"os"
)

func openAdGuardTerminal(_, _ *os.File) (adguardTerminal, error) {
	return nil, errors.New("interactive AdGuard Home connection is available only on macOS")
}
