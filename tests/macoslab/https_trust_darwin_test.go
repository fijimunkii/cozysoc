package macoslab

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Never run this on a developer host. The shell creates this marker only after
// checking GitHub's disposable hosted-runner environment, before launching Terminal.
func trustHTTPSFixture(t *testing.T, work string, der []byte, name string) {
	t.Helper()
	marker, err := os.ReadFile(filepath.Join(work, "disposable-trust-allowed"))
	if err != nil || string(marker) != "github-hosted\n" {
		t.Fatal("trusted TLS lab requires disposable hosted CI")
	}
	certPath := filepath.Join(work, "https-trust.pem")
	sum := sha256.Sum256(der)
	fingerprint := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(work, "https-trust.sha256"), []byte(fingerprint+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	// Journal precedes mutation. The outer shell repeats cleanup after a killed test.
	security := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "/usr/bin/python3", "-c", "import subprocess,sys; sys.exit(subprocess.run(['/usr/bin/security',*sys.argv[1:]],timeout=10).returncode)"}, args...)...).Run()
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "/usr/bin/python3", filepath.Join(work, "trust-cleanup.py")).Run(); err != nil {
			t.Error("remove fixture trust/certificate failed")
			return
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err = cert.Verify(x509.VerifyOptions{DNSName: name}); err == nil {
			t.Error("fixture remained trusted after cleanup")
			return
		}
		if err := os.WriteFile(filepath.Join(work, "https-trust-removed"), []byte("1\n"), 0600); err != nil {
			t.Error(err)
		}
	})
	if err := security("add-trusted-cert", "-d", "-r", "trustRoot", "-p", "ssl", "-s", name, "-k", "/Library/Keychains/System.keychain", certPath); err != nil {
		t.Fatal("install scoped disposable TLS trust failed")
	}
}
