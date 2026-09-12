package httpsexchange

import (
	"io"
	"net"
)

// TLS performs stream I/O synchronously here. Cancellation only closes the socket.
// Ceilings include handshake, alerts, application records and TLS read-ahead.
type boundedConn struct {
	net.Conn
	readLeft, writeLeft, readCallsLeft, writeCallsLeft int
	readBytes, writeBytes, readCalls, writeCalls       int
}

func (c *boundedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.readLeft <= 0 || c.readCallsLeft <= 0 {
		return 0, ErrBudget
	}
	p = p[:min(len(p), c.readLeft)]
	c.readCalls++
	c.readCallsLeft--
	n, err := c.Conn.Read(p)
	if n < 0 || n > len(p) {
		return 0, ErrTransport
	}
	c.readBytes += n
	c.readLeft -= n
	if n == 0 && err == nil {
		return 0, io.ErrNoProgress
	}
	return n, err
}
func (c *boundedConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > c.writeLeft || c.writeCallsLeft <= 0 {
		return 0, ErrBudget
	}
	c.writeCalls++
	c.writeCallsLeft--
	n, err := c.Conn.Write(p)
	if n < 0 || n > len(p) {
		return 0, ErrTransport
	}
	c.writeBytes += n
	c.writeLeft -= n
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}
