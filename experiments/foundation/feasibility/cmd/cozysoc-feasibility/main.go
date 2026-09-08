package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	schemaVersion      = 1
	maxCaptureCount    = 1000
	maxCaptureDuration = 60 * time.Second
	minHeartbeat       = 100 * time.Millisecond
	maxHeartbeat       = time.Minute
)

type interfaceSnapshot struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	MTU   int    `json:"mtu"`
	Flags string `json:"flags"`
}

type hostSnapshot struct {
	SchemaVersion int                 `json:"schema_version"`
	OS            string              `json:"os"`
	Arch          string              `json:"arch"`
	GoVersion     string              `json:"go_version"`
	Interfaces    []interfaceSnapshot `json:"interfaces"`
	TCPDump       bool                `json:"tcpdump_available"`
}

type packetObservation struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	SourceID      string    `json:"source_id"`
	ScopeID       string    `json:"scope_id"`
	Interface     string    `json:"interface"`
	ObservedAt    time.Time `json:"observed_at"`
	SummarySHA256 string    `json:"summary_sha256"`
	SummaryBytes  int       `json:"summary_bytes"`
	Summary       string    `json:"summary,omitempty"`
}

type heartbeatRecord struct {
	SchemaVersion      int       `json:"schema_version"`
	Kind               string    `json:"kind"`
	ObservedAt         time.Time `json:"observed_at"`
	PID                int       `json:"pid"`
	Sequence           uint64    `json:"sequence"`
	ElapsedMS          int64     `json:"elapsed_ms,omitempty"`
	ExpectedIntervalMS int64     `json:"expected_interval_ms"`
	Gap                bool      `json:"gap"`
}

type gapSummary struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Records       int    `json:"records"`
	Gaps          int    `json:"gaps"`
	MaxElapsedMS  int64  `json:"max_elapsed_ms"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "host":
		if len(args) != 1 {
			return fmt.Errorf("host takes no arguments")
		}
		return runHost(stdout)
	case "service":
		return runServiceCommand(ctx, args[1:], stdout, stderr)
	case "gaps":
		return runGapsCommand(args[1:], stdout)
	case "capture":
		return runCaptureCommand(ctx, args[1:], stdout)
	case "normalize":
		return runNormalizeCommand(args[1:], stdin, stdout)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usageText())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usageText())
	}
}

func usageError() error {
	return errors.New(usageText())
}

func usageText() string {
	return `cozysoc-feasibility validates Foundation assumptions without becoming product code.

Usage:
  cozysoc-feasibility host
  cozysoc-feasibility service --heartbeat PATH [--interval 1s]
  cozysoc-feasibility gaps --heartbeat PATH
  cozysoc-feasibility capture --interface IFACE [--count 10] [--timeout 15s] [--show-summary]
  cozysoc-feasibility normalize [--interface IFACE] [--show-summary] < tcpdump.txt

Capture and normalize redact packet summaries by default and emit a SHA-256 digest instead.
`
}

func runHost(w io.Writer) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}

	snapshot := hostSnapshot{
		SchemaVersion: schemaVersion,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		GoVersion:     runtime.Version(),
		Interfaces:    make([]interfaceSnapshot, 0, len(interfaces)),
	}
	if _, err := exec.LookPath("tcpdump"); err == nil {
		snapshot.TCPDump = true
	}

	for _, iface := range interfaces {
		snapshot.Interfaces = append(snapshot.Interfaces, interfaceSnapshot{
			Name:  iface.Name,
			Index: iface.Index,
			MTU:   iface.MTU,
			Flags: iface.Flags.String(),
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(snapshot)
}

func runServiceCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(stderr)
	heartbeatPath := fs.String("heartbeat", "", "append heartbeat JSONL to this file")
	interval := fs.Duration("interval", time.Second, "heartbeat interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("service takes flags only")
	}
	if *heartbeatPath == "" {
		return fmt.Errorf("--heartbeat is required")
	}
	if *interval < minHeartbeat || *interval > maxHeartbeat {
		return fmt.Errorf("--interval must be between %s and %s", minHeartbeat, maxHeartbeat)
	}

	return runService(ctx, *heartbeatPath, *interval, stdout)
}

func runService(ctx context.Context, path string, interval time.Duration, stdout io.Writer) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open heartbeat file: %w", err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	started := time.Now().UTC()
	if _, err := fmt.Fprintf(stdout, "service pid=%d heartbeat=%s started=%s\n", os.Getpid(), path, started.Format(time.RFC3339Nano)); err != nil {
		return err
	}

	var sequence uint64
	var previous time.Time
	write := func(now time.Time) error {
		sequence++
		record := makeHeartbeat(previous, now.UTC(), interval, sequence, os.Getpid())
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("write heartbeat: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync heartbeat: %w", err)
		}
		previous = now.UTC()
		return nil
	}

	if err := write(time.Now()); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := write(now); err != nil {
				return err
			}
		}
	}
}

func makeHeartbeat(previous, now time.Time, interval time.Duration, sequence uint64, pid int) heartbeatRecord {
	record := heartbeatRecord{
		SchemaVersion:      schemaVersion,
		Kind:               "service_heartbeat",
		ObservedAt:         now,
		PID:                pid,
		Sequence:           sequence,
		ExpectedIntervalMS: interval.Milliseconds(),
	}
	if !previous.IsZero() {
		elapsed := now.Sub(previous)
		record.ElapsedMS = elapsed.Milliseconds()
		record.Gap = elapsed > interval*3
	}
	return record
}

func runGapsCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("gaps", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	heartbeatPath := fs.String("heartbeat", "", "heartbeat JSONL file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *heartbeatPath == "" {
		return fmt.Errorf("gaps requires --heartbeat PATH")
	}

	file, err := os.Open(*heartbeatPath)
	if err != nil {
		return fmt.Errorf("open heartbeat file: %w", err)
	}
	defer file.Close()

	summary := gapSummary{SchemaVersion: schemaVersion, Kind: "heartbeat_gap_summary"}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record heartbeatRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("parse heartbeat line %d: %w", summary.Records+1, err)
		}
		summary.Records++
		if record.Gap {
			summary.Gaps++
		}
		if record.ElapsedMS > summary.MaxElapsedMS {
			summary.MaxElapsedMS = record.ElapsedMS
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read heartbeat file: %w", err)
	}

	return json.NewEncoder(stdout).Encode(summary)
}

func runCaptureCommand(parent context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	iface := fs.String("interface", "", "network interface to capture")
	count := fs.Int("count", 10, "number of packet summaries")
	timeout := fs.Duration("timeout", 15*time.Second, "capture timeout")
	showSummary := fs.Bool("show-summary", false, "include sensitive tcpdump summary text")
	sourceID := fs.String("source-id", "local-capture", "observation source identifier")
	scopeID := fs.String("scope-id", "authorized-lab", "authorized scope identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("capture takes flags only")
	}
	if err := validateCaptureOptions(*iface, *count, *timeout); err != nil {
		return err
	}
	if err := validateInterface(*iface); err != nil {
		return err
	}

	tcpdump, err := exec.LookPath("tcpdump")
	if err != nil {
		return fmt.Errorf("tcpdump not found; install/use the platform capture tool before running this experiment")
	}

	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tcpdump, "-n", "-l", "-q", "-tt", "-i", *iface, "-c", strconv.Itoa(*count))
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("tcpdump stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tcpdump: %w", err)
	}

	if err := normalizeLines(stdoutPipe, stdout, *iface, *sourceID, *scopeID, *showSummary); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("capture timed out after %s; verify traffic is present and the interface is correct", *timeout)
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("tcpdump capture failed; verify capture permissions and interface %q: %s", *iface, message)
	}
	return nil
}

func validateCaptureOptions(iface string, count int, timeout time.Duration) error {
	if iface == "" {
		return fmt.Errorf("--interface is required")
	}
	if count < 1 || count > maxCaptureCount {
		return fmt.Errorf("--count must be between 1 and %d", maxCaptureCount)
	}
	if timeout <= 0 || timeout > maxCaptureDuration {
		return fmt.Errorf("--timeout must be > 0 and <= %s", maxCaptureDuration)
	}
	return nil
}

func validateInterface(name string) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	for _, iface := range interfaces {
		if iface.Name == name {
			return nil
		}
	}
	return fmt.Errorf("interface %q does not exist on this host", name)
}

func runNormalizeCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	iface := fs.String("interface", "fixture", "interface label")
	showSummary := fs.Bool("show-summary", false, "include sensitive tcpdump summary text")
	sourceID := fs.String("source-id", "fixture", "observation source identifier")
	scopeID := fs.String("scope-id", "fixture", "scope identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("normalize takes flags only")
	}
	return normalizeLines(stdin, stdout, *iface, *sourceID, *scopeID, *showSummary)
}

func normalizeLines(r io.Reader, w io.Writer, iface, sourceID, scopeID string, showSummary bool) error {
	scanner := bufio.NewScanner(r)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		observation := normalizeTcpdumpLine(line, iface, sourceID, scopeID, showSummary)
		if err := enc.Encode(observation); err != nil {
			return fmt.Errorf("encode observation: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read packet summary: %w", err)
	}
	return nil
}

func normalizeTcpdumpLine(line, iface, sourceID, scopeID string, showSummary bool) packetObservation {
	digest := sha256.Sum256([]byte(line))
	observation := packetObservation{
		SchemaVersion: schemaVersion,
		Kind:          "packet_summary",
		SourceID:      sourceID,
		ScopeID:       scopeID,
		Interface:     iface,
		ObservedAt:    parseTcpdumpTimestamp(line),
		SummarySHA256: hex.EncodeToString(digest[:]),
		SummaryBytes:  len([]byte(line)),
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	if showSummary {
		observation.Summary = line
	}
	return observation
}

func parseTcpdumpTimestamp(line string) time.Time {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Time{}
	}
	sec := int64(seconds)
	nsec := int64((seconds - float64(sec)) * float64(time.Second))
	return time.Unix(sec, nsec).UTC()
}
