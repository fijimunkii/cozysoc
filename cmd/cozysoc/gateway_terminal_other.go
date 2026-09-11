//go:build !darwin

package main

import (
	"errors"
	"os"
)

func openGatewayTerminal(_, _ *os.File) (gatewayCheckTerminal, error) {
	return nil, errors.New("experimental interactive gateway checks are available only on macOS; no controller connection was opened")
}
