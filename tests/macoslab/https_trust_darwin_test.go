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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "/usr/bin/security"}, args...)...).Run()
	}
	t.Cleanup(func() {
		if err := security("remove-trusted-cert", "-d", certPath); err != nil {
			t.Error("remove fixture trust failed")
			return
		}
		if err := security("delete-certificate", "-Z", fingerprint, "/Library/Keychains/System.keychain"); err != nil {
			t.Error("remove fixture certificate failed")
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
