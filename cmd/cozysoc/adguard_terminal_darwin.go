//go:build darwin

package main

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

func openAdGuardTerminal(input, output *os.File) (adguardTerminal, error) {
	base, err := openGatewayTerminal(input, output)
	if err != nil {
		return nil, err
	}
	return base.(*darwinGatewayTerminal), nil
}

// ReadSecretLine disables terminal echo only while reading one bounded canonical
// line. Restoration is mandatory before the secret can be submitted.
func (t *darwinGatewayTerminal) ReadSecretLine(ctx context.Context) (secret []byte, err error) {
	if !foregroundGatewayTerminal(t.fd) {
		return nil, errGatewayTerminal
	}
	original, err := unix.IoctlGetTermios(t.fd, unix.TIOCGETA)
	if err != nil {
		return nil, errGatewayTerminal
	}
	noEcho := *original
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(t.fd, unix.TIOCSETA, &noEcho); err != nil {
		return nil, errGatewayTerminal
	}
	defer func() {
		if restoreErr := unix.IoctlSetTermios(t.fd, unix.TIOCSETA, original); restoreErr != nil {
			err = errGatewayTerminal
		}
		if flushErr := t.FlushInput(); flushErr != nil {
			err = errGatewayTerminal
		}
		if err != nil {
			for i := range secret {
				secret[i] = 0
			}
			secret = nil
		}
	}()
	var data [1025]byte
	defer func() {
		for i := range data {
			data[i] = 0
		}
	}()
	used := 0
	for used < len(data) {
		if err := t.wait(ctx, false); err != nil {
			return nil, err
		}
		n, readErr := unix.Read(t.fd, data[used:])
		if readErr == unix.EAGAIN || readErr == unix.EINTR {
			continue
		}
		if readErr != nil || n == 0 {
			return nil, errGatewayTerminal
		}
		used += n
		if err := gatewayPromptContext(ctx); err != nil {
			return nil, err
		}
		if data[used-1] == '\n' {
			value := data[:used-1]
			if len(value) > 0 && value[len(value)-1] == '\r' {
				value = value[:len(value)-1]
			}
			if len(value) == 0 || len(value) > 1024 {
				return nil, errGatewayTerminal
			}
			return append([]byte(nil), value...), nil
		}
	}
	return nil, errGatewayTerminal
}
