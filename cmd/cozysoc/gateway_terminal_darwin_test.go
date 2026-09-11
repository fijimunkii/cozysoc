package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These native syscall tests use private pipes, never terminal input or packets.
// The isolated PTY lab separately exercises /dev/tty through the built command.
func TestGatewayDescriptorWaitBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, fd := range []int{-1, unix.FD_SETSIZE, int(^uint(0) >> 1)} {
		if err := waitGatewayDescriptor(ctx, fd, false); err != errGatewayTerminal {
			t.Fatal("unbounded descriptor reached select", fd, err)
		}
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := waitGatewayDescriptor(canceled, -1, false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not checked before descriptor access", err)
	}
	if err := waitGatewayDescriptor(context.Background(), 0, false); err == nil {
		t.Fatal("unbounded context accepted")
	}
}

func TestGatewayDescriptorWaitReadAndWrite(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitGatewayDescriptor(ctx, pipe[1], true); err != nil {
		t.Fatal("writable descriptor rejected", err)
	}
	if _, err := unix.Write(pipe[1], []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := waitGatewayDescriptor(ctx, pipe[0], false); err != nil {
		t.Fatal("readable descriptor rejected", err)
	}
}
