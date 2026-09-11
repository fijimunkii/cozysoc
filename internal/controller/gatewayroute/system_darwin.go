package gatewayroute

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"syscall"
	"time"
)

func NewInspector() Inspector {
	return inspector{lookup: lookupSystem, readInterface: readSystemInterface, now: time.Now}
}

func lookupSystem(ctx context.Context, target netip.Addr) (route, error) {
	if err := ctx.Err(); err != nil {
		return route{}, err
	}
	if syscall.SizeofRtMsghdr != routeHeaderBytes || syscall.RTM_VERSION != routeVersion ||
		syscall.RTM_GET != routeGet || syscall.AF_INET != afInet || syscall.AF_LINK != afLink {
		return route{}, ErrUnsupported
	}
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return route{}, ErrUnavailable
	}
	seq := binary.LittleEndian.Uint32(nonce[:])&0x7fffffff | 1
	pid := uint32(os.Getpid())
	request, err := getRequest(target, pid, seq)
	if err != nil {
		return route{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// Socket is metadata-only AF_ROUTE, never AF_INET/ICMP/UDP. Protect close-on-exec
	// setup against concurrent subprocess creation in the existing controller.
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_INET)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return route{}, safeSocketError(err)
	}
	defer syscall.Close(fd)
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, maxRouteMessageBytes*16); err != nil {
		return route{}, safeSocketError(err)
	}
	timeout := syscall.NsecToTimeval((50 * time.Millisecond).Nanoseconds())
	for _, option := range []int{syscall.SO_RCVTIMEO, syscall.SO_SNDTIMEO} {
		if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, option, &timeout); err != nil {
			return route{}, safeSocketError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return route{}, err
	}
	if n, err := syscall.Write(fd, request); err != nil || n != len(request) {
		return route{}, safeSocketError(err)
	}
	buffer := make([]byte, maxRouteMessageBytes+1)
	// Bound unrelated route notifications as well as wait time; never retry a
	// query or retain the routing/neighbor table. Kernel calls are synchronous.
	for received := 0; received < 16; {
		if err := ctx.Err(); err != nil {
			return route{}, err
		}
		// Read avoids Sockaddr conversion: Go's Recvfrom does not decode AF_ROUTE.
		n, err := syscall.Read(fd, buffer)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return route{}, safeSocketError(err)
		}
		received++
		r, matched, err := parseReply(buffer[:n], pid, seq)
		if err != nil {
			return route{}, err
		}
		if err := ctx.Err(); err != nil {
			return route{}, err
		}
		if matched {
			return r, nil
		}
	}
	return route{}, ErrUnavailable
}

func safeSocketError(err error) error {
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		return ErrPermission
	}
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return ErrNoRoute
	}
	return ErrUnavailable
}
