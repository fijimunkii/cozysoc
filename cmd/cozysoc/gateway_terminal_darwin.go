package main

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// Own an independently opened nonblocking controlling-terminal descriptor. Never
// set O_NONBLOCK on stdin's shared open-file description, change termios, launch
// a shell, or leave a goroutine blocked in Read after an expired review.
type darwinGatewayTerminal struct{ fd int }

func foregroundGatewayTerminal(fd int) bool {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil || t.Lflag&(unix.ICANON|unix.ISIG) != unix.ICANON|unix.ISIG {
		return false
	}
	group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && group == unix.Getpgrp()
}

func openGatewayTerminal(input, output *os.File) (gatewayCheckTerminal, error) {
	if os.Geteuid() == 0 || input == nil || output == nil ||
		!foregroundGatewayTerminal(int(input.Fd())) || !foregroundGatewayTerminal(int(output.Fd())) {
		return nil, errGatewayTerminal
	}
	// stdin and stdout must refer to the same terminal device, not a file/pipe or
	// an unrelated output terminal hiding the reviewed selection.
	var in, out unix.Stat_t
	if unix.Fstat(int(input.Fd()), &in) != nil || unix.Fstat(int(output.Fd()), &out) != nil || in.Rdev != out.Rdev {
		return nil, errGatewayTerminal
	}
	fd, err := unix.Open("/dev/tty", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errGatewayTerminal
	}
	if !foregroundGatewayTerminal(fd) {
		_ = unix.Close(fd)
		return nil, errGatewayTerminal
	}
	return &darwinGatewayTerminal{fd: fd}, nil
}

func (t *darwinGatewayTerminal) Close() error { return unix.Close(t.fd) }

func (t *darwinGatewayTerminal) FlushInput() error {
	if !foregroundGatewayTerminal(t.fd) {
		return errGatewayTerminal
	}
	// Darwin TIOCFLUSH takes an int pointer; FREAD=1 flushes only unread input,
	// including incomplete canonical lines. Output and terminal modes stay intact.
	if unix.IoctlSetPointerInt(t.fd, unix.TIOCFLUSH, 1) != nil {
		return errGatewayTerminal
	}
	return nil
}

func (t *darwinGatewayTerminal) wait(ctx context.Context, writing bool) error {
	if err := gatewayPromptContext(ctx); err != nil {
		return err
	}
	if !foregroundGatewayTerminal(t.fd) {
		return errGatewayTerminal
	}
	return waitGatewayDescriptor(ctx, t.fd, writing)
}

// Darwin's poll/kqueue path does not support the indirect /dev/tty device.
// select reaches its terminal driver instead. Bound fd before constructing the
// fixed-size sets and wait at most 50 ms, without making the actual I/O blocking.
func waitGatewayDescriptor(ctx context.Context, fd int, writing bool) error {
	if err := gatewayPromptContext(ctx); err != nil {
		return err
	}
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return errGatewayTerminal
	}
	var ready, exceptional unix.FdSet
	ready.Set(fd)
	exceptional.Set(fd)
	var read, write *unix.FdSet
	if writing {
		write = &ready
	} else {
		read = &ready
	}
	timeout := unix.Timeval{Usec: 50000}
	_, err := unix.Select(fd+1, read, write, &exceptional, &timeout)
	if contextErr := gatewayPromptContext(ctx); contextErr != nil {
		return contextErr
	}
	if err == unix.EINTR {
		return nil // Sets are unspecified after interruption; recheck on next I/O.
	}
	if err != nil || exceptional.IsSet(fd) {
		return errGatewayTerminal
	}
	return nil
}

func (t *darwinGatewayTerminal) Write(ctx context.Context, text string) error {
	if len(text) > 8192 {
		return errGatewayTerminal
	}
	data := []byte(text)
	for len(data) > 0 {
		if err := gatewayPromptContext(ctx); err != nil {
			return err
		}
		if !foregroundGatewayTerminal(t.fd) {
			return errGatewayTerminal
		}
		n, err := unix.Write(t.fd, data)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN {
			if err := t.wait(ctx, true); err != nil {
				return err
			}
			continue
		}
		if err != nil || n <= 0 {
			return errGatewayTerminal
		}
		data = data[n:]
	}
	return gatewayPromptContext(ctx)
}

func (t *darwinGatewayTerminal) ReadLine(ctx context.Context) (line string, err error) {
	// A single bounded canonical line; no unbounded scanner or reader goroutine.
	// Flush any trailing/type-ahead data before returning control to the shell.
	defer func() {
		if flushErr := t.FlushInput(); flushErr != nil {
			line, err = "", flushErr
		}
	}()
	var data [128]byte
	used := 0
	for used < len(data) {
		if err := t.wait(ctx, false); err != nil {
			return "", err
		}
		n, err := unix.Read(t.fd, data[used:])
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return "", errGatewayTerminal // EOF, disconnect and errors never approve.
		}
		used += n
		if err := gatewayPromptContext(ctx); err != nil {
			return "", err
		}
		if data[used-1] == '\n' {
			return string(data[:used]), nil
		}
	}
	return "", errGatewayTerminal
}
