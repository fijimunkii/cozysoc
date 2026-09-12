package httpsexchange

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type memoryConn struct {
	read    *bytes.Reader
	written bytes.Buffer
	short   bool
}

func (c *memoryConn) Read(p []byte) (int, error) { return c.read.Read(p) }
func (c *memoryConn) Write(p []byte) (int, error) {
	if c.short && len(p) > 1 {
		p = p[:1]
	}
	return c.written.Write(p)
}
func (c *memoryConn) Close() error                     { return nil }
func (c *memoryConn) LocalAddr() net.Addr              { return nil }
func (c *memoryConn) RemoteAddr() net.Addr             { return nil }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }
func TestTransportCeilingsBeforeUnderlyingIO(t *testing.T) {
	raw := &memoryConn{read: bytes.NewReader([]byte("secret beyond budget"))}
	c := &boundedConn{Conn: raw, readLeft: 3, writeLeft: 3, readCallsLeft: 2, writeCallsLeft: 2}
	p := make([]byte, 32)
	n, err := c.Read(p)
	if err != nil || n != 3 || c.readBytes != 3 || raw.read.Len() != len("secret beyond budget")-3 {
		t.Fatal("read exceeded exact bytes")
	}
	if n, err := c.Read(p); n != 0 || !errors.Is(err, ErrBudget) || c.readCalls != 1 {
		t.Fatal("read after exhaustion")
	}
	if n, err := c.Write([]byte("four")); n != 0 || !errors.Is(err, ErrBudget) || raw.written.Len() != 0 || c.writeCalls != 0 {
		t.Fatal("oversized write reached transport")
	}
	if n, err := c.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, ErrBudget) || raw.written.String() != "abc" {
		t.Fatal("write after exhaustion")
	}
}
func TestTransportCallCeilingsAndPartialWrite(t *testing.T) {
	raw := &memoryConn{read: bytes.NewReader([]byte("abcdef")), short: true}
	c := &boundedConn{Conn: raw, readLeft: 10, writeLeft: 10, readCallsLeft: 1, writeCallsLeft: 1}
	if n, err := c.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, ErrBudget) || c.readBytes != 1 {
		t.Fatal("read call ceiling")
	}
	if n, err := c.Write([]byte("abc")); n != 1 || !errors.Is(err, io.ErrShortWrite) || c.writeBytes != 1 {
		t.Fatal("partial write lost", n, err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, ErrBudget) || c.writeCalls != 1 {
		t.Fatal("retried partial write")
	}
}
func TestHeaderLimitIncludesEveryInformationalHeader(t *testing.T) {
	header := []byte("HTTP/1.1 204 No Content\r\n\r\n")
	for _, limit := range []int{len(header) - 1, len(header)} {
		got, err := readHeader(bufio.NewReader(bytes.NewReader(header)), limit)
		if len(got) != limit {
			t.Fatal("header byte count")
		}
		if limit == len(header) && err != nil {
			t.Fatal(err)
		}
		if limit < len(header) && !errors.Is(err, ErrBudget) {
			t.Fatal("accepted truncated header")
		}
	}
}
