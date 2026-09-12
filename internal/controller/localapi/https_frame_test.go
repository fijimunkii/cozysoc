package localapi

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHTTPSFrameBounds(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{strings.Repeat("x", httpsFrameLimit-1) + "\n", true},
		{strings.Repeat("x", httpsFrameLimit) + "\n", false},
		{strings.Repeat("x", httpsFrameLimit*2), false},
		{"{}", false},
	} {
		_, err := httpsFrame(bufio.NewReaderSize(strings.NewReader(tc.raw), httpsFrameLimit+1))
		if (err == nil) != tc.valid {
			t.Fatalf("frame len %d: %v", len(tc.raw), err)
		}
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	_ = server.SetDeadline(time.Now().Add(time.Second))
	if err := writeHTTPSResponse(server, "https-check", strings.Repeat("x", httpsFrameLimit)); err != errHTTPSProtocol {
		t.Fatal("oversized output reached write", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- writeHTTPSResponse(server, "https-check", map[string]string{"value": strings.Repeat("x", gatewayFrameLimit+1)})
	}()
	if _, err := readHTTPSResponse(bufio.NewReaderSize(client, httpsFrameLimit+1), "https-check"); err != nil {
		t.Fatal("valid large response failed", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
