package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTcpdumpLineRedactsByDefault(t *testing.T) {
	line := "1788900000.123456 IP 192.0.2.10.12345 > 198.51.100.20.443: tcp 0"
	got := normalizeTcpdumpLine(line, "en0", "desktop", "lab", false)

	if got.Kind != "packet_summary" || got.Interface != "en0" {
		t.Fatalf("unexpected observation: %+v", got)
	}
	if got.Summary != "" {
		t.Fatalf("summary leaked by default: %q", got.Summary)
	}
	if got.SummarySHA256 == "" || got.SummaryBytes != len([]byte(line)) {
		t.Fatalf("missing summary digest metadata: %+v", got)
	}
	if got.ObservedAt.Unix() != 1788900000 {
		t.Fatalf("unexpected parsed timestamp: %s", got.ObservedAt)
	}
}

func TestNormalizeCanShowSummaryExplicitly(t *testing.T) {
	line := "1788900000.500000 IP6 2001:db8::1 > 2001:db8::2: ICMP6"
	got := normalizeTcpdumpLine(line, "eth0", "sensor", "lab", true)
	if got.Summary != line {
		t.Fatalf("expected summary %q, got %q", line, got.Summary)
	}
}

func TestNormalizeCommandProducesJSONL(t *testing.T) {
	input := strings.NewReader("1788900000.100000 IP 192.0.2.1 > 198.51.100.1: tcp 0\n\n")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"normalize", "--interface", "fixture0"}, input, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var got packetObservation
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got); err != nil {
		t.Fatalf("parse output: %v\n%s", err, out.String())
	}
	if got.Interface != "fixture0" || got.Summary != "" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestHeartbeatGapDetection(t *testing.T) {
	interval := time.Second
	previous := time.Unix(100, 0).UTC()

	regular := makeHeartbeat(previous, previous.Add(2*time.Second), interval, 2, 42)
	if regular.Gap {
		t.Fatalf("2x interval should not be a gap: %+v", regular)
	}

	gap := makeHeartbeat(previous, previous.Add(4*time.Second), interval, 2, 42)
	if !gap.Gap || gap.ElapsedMS != 4000 {
		t.Fatalf("expected gap: %+v", gap)
	}
}

func TestCaptureBounds(t *testing.T) {
	if err := validateCaptureOptions("en0", 1, time.Second); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	if err := validateCaptureOptions("", 1, time.Second); err == nil {
		t.Fatal("missing interface accepted")
	}
	if err := validateCaptureOptions("en0", maxCaptureCount+1, time.Second); err == nil {
		t.Fatal("oversized packet count accepted")
	}
	if err := validateCaptureOptions("en0", 1, maxCaptureDuration+time.Second); err == nil {
		t.Fatal("oversized duration accepted")
	}
}

func TestHostSnapshotDoesNotIncludeAddresses(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"host"}, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "hardware_addr") || strings.Contains(text, "addresses") {
		t.Fatalf("host snapshot unexpectedly exposes addresses: %s", text)
	}
}
