//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestSMTPPodman_DeliversAcrossTLSModes runs Mailpit in a podman container per
// subtest and exercises the TLS modes the in-process fake in notify_test.go
// can't speak: real STARTTLS, real implicit TLS, and AUTH LOGIN. The plain
// subtest is included as a sanity check that go-mail actually talks to a
// standards-compliant server rather than just our hand-rolled fake.
func TestSMTPPodman_DeliversAcrossTLSModes(t *testing.T) {
	requirePodman(t)

	cases := []struct {
		name    string
		tlsMode string // RunWisp TOML tls= value
		mpMode  mailpitTLSMode
		useAuth bool
	}{
		{name: "Plain", tlsMode: "none", mpMode: mailpitTLSOff, useAuth: false},
		{name: "STARTTLS_AUTH", tlsMode: "starttls", mpMode: mailpitTLSStartTLS, useAuth: true},
		{name: "ImplicitTLS_AUTH", tlsMode: "implicit", mpMode: mailpitTLSImplicit, useAuth: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := mailpitOptions{TLS: tc.mpMode}
			if tc.useAuth {
				opts.AuthUser = "e2e"
				opts.AuthPass = "secret"
			}
			mp := startMailpit(t, opts)

			authBlock := ""
			if tc.useAuth {
				authBlock = "username        = \"e2e\"\npassword        = \"secret\"\n"
			}

			configPath := writeNotifyConfig(t, fmt.Sprintf(`
[notify]
coalesce_window = "1m"

[tasks.fail-task]
run = "exit 1"

[[notifier]]
id              = "email-ops"
type            = "smtp"
host            = "127.0.0.1"
port            = %s
tls             = "%s"
tls_skip_verify = true
%sfrom            = "RunWisp <runwisp@example.test>"
to              = ["alerts@example.test"]

[[notification_route]]
match  = { kind = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["email-ops"]
`, mp.smtpPort, tc.tlsMode, authBlock))

			projectDir := runwispProjectDir(t)
			binaryPath := buildRunwispBinary(t, projectDir)
			daemon := startDaemon(t, projectDir, binaryPath, configPath)

			client := socketClient(t, daemon.dataDir)
			_, err := client.TriggerRun("fail-task")
			require.NoError(t, err)

			msgs := mp.WaitForMessages(t, 1, 15*time.Second)
			raw := mp.MessageRaw(t, msgs[0].ID)
			require.Contains(t, raw, "fail-task",
				"SMTP DATA should mention the failing task")
			require.Contains(t, raw, "multipart/alternative",
				"must be multipart/alternative")
			require.Contains(t, raw, "text/plain",
				"must include the plain-text alternative")
			require.Contains(t, raw, "text/html",
				"must include the HTML alternative")
		})
	}
}

// TestSMTPPodman_AuthFailureSurfacesDeliveryFailed pairs Mailpit's real AUTH
// rejection (Mailpit returns 535) with the daemon's delivery-failure
// synthesizer: wrong password → retries exhaust → notify.delivery_failed
// in-app row. Mailpit must end up with zero accepted messages.
func TestSMTPPodman_AuthFailureSurfacesDeliveryFailed(t *testing.T) {
	requirePodman(t)

	mp := startMailpit(t, mailpitOptions{
		TLS:      mailpitTLSStartTLS,
		AuthUser: "e2e",
		AuthPass: "secret",
	})

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[tasks.fail-task]
run = "exit 1"

[[notifier]]
id              = "email-ops"
type            = "smtp"
host            = "127.0.0.1"
port            = %s
tls             = "starttls"
tls_skip_verify = true
username        = "e2e"
password        = "WRONG"
from            = "RunWisp <runwisp@example.test>"
to              = ["alerts@example.test"]

[[notification_route]]
match  = { kind = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["email-ops", "inapp"]
`, mp.smtpPort))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)
	_, err := client.TriggerRun("fail-task")
	require.NoError(t, err)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		page, err := client.ListNotifications(50, "")
		require.NoError(t, err)
		if hasDeliveryFailure(page.Items) {
			require.Empty(t, mp.Messages(t),
				"Mailpit must have received zero messages — auth was supposed to fail")
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	page, _ := client.ListNotifications(50, "")
	t.Fatalf("notify.delivery_failed never appeared. mailpit messages=%d, items=%+v",
		len(mp.Messages(t)), page.Items)
}

// ---------- Mailpit container helpers ----------

// requirePodman gates the test on a usable podman install. It skips silently
// when podman is unavailable so `make ci` on machines / CI runners without
// podman still passes — the existing in-process fake covers the no-deps path.
func requirePodman(t *testing.T) {
	t.Helper()
	if os.Getenv("RUNWISP_SKIP_PODMAN_TESTS") != "" {
		t.Skip("RUNWISP_SKIP_PODMAN_TESTS set")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed; skipping SMTP podman e2e")
	}
	if out, err := exec.Command("podman", "info").CombinedOutput(); err != nil {
		t.Skipf("podman info failed (rootless socket not ready?): %s", string(out))
	}
}

// mailpitTLSMode picks the SMTP listener mode for a Mailpit instance. Mailpit
// has a single SMTP listener whose TLS behaviour is set at process start: off,
// STARTTLS-capable, or implicit TLS (SMTPS). Each subtest spins its own
// container so the modes don't have to coexist.
type mailpitTLSMode int

const (
	mailpitTLSOff mailpitTLSMode = iota
	mailpitTLSStartTLS
	mailpitTLSImplicit
)

type mailpitOptions struct {
	TLS mailpitTLSMode
	// AuthUser+AuthPass enable MP_SMTP_AUTH_FILE with a single bcrypt entry.
	// Leave empty to run without auth.
	AuthUser string
	AuthPass string
}

type mailpitContainer struct {
	name     string
	smtpPort string // host port mapped to MP_SMTP_BIND_ADDR=:1025
	apiURL   string
}

type mailpitMessage struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
}

type mailpitMessagesPage struct {
	Messages []mailpitMessage `json:"messages"`
	Total    int              `json:"total"`
}

func startMailpit(t *testing.T, opts mailpitOptions) *mailpitContainer {
	t.Helper()

	smtpHostPort := reserveTCPPort(t)
	apiHostPort := reserveTCPPort(t)

	name := fmt.Sprintf("runwisp-mailpit-%d-%d", os.Getpid(), time.Now().UnixNano())

	args := []string{
		"run", "-d", "--rm",
		"--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:1025", smtpHostPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:8025", apiHostPort),
		"-e", "MP_SMTP_BIND_ADDR=:1025",
		"-e", "MP_API_BIND_ADDR=:8025",
	}

	if opts.TLS != mailpitTLSOff {
		tlsDir := relaxedTempDir(t)
		generateSelfSignedTLS(t, tlsDir)
		args = append(args,
			"-v", tlsDir+":/tls:Z",
			"-e", "MP_SMTP_TLS_CERT=/tls/cert.pem",
			"-e", "MP_SMTP_TLS_KEY=/tls/key.pem",
		)
		if opts.TLS == mailpitTLSImplicit {
			args = append(args, "-e", "MP_SMTP_REQUIRE_TLS=true")
		}
	}

	if opts.AuthUser != "" {
		authDir := relaxedTempDir(t)
		writeMailpitAuthFile(t, authDir, opts.AuthUser, opts.AuthPass)
		args = append(args,
			"-v", authDir+":/auth:Z",
			"-e", "MP_SMTP_AUTH_FILE=/auth/passwd",
		)
		// Auth on the plaintext SMTP listener needs the insecure flag, but
		// Mailpit refuses to start when it's set together with require-TLS.
		// Skip it whenever TLS will enforce encryption — AUTH over TLS is
		// allowed by default.
		if opts.TLS == mailpitTLSOff {
			args = append(args, "-e", "MP_SMTP_AUTH_ALLOW_INSECURE=true")
		}
	}

	args = append(args, "docker.io/axllent/mailpit:latest")

	// `podman run` pulls the image on first boot. Bound it so a slow or stalled
	// registry fails this test fast instead of starving the whole go-test
	// timeout (a hung pull once ate the full 10m CI budget).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "podman", args...).CombinedOutput()
	require.NoErrorf(t, err, "podman run failed: %s", string(out))

	t.Cleanup(func() {
		killCmd := exec.Command("podman", "kill", name)
		_, _ = killCmd.CombinedOutput()
	})

	c := &mailpitContainer{
		name:     name,
		smtpPort: strconv.Itoa(smtpHostPort),
		apiURL:   fmt.Sprintf("http://127.0.0.1:%d", apiHostPort),
	}
	// First boot pulls the image (~30 MB); give it generous headroom. After
	// the image is cached, subsequent containers come up in 2–3 s.
	c.WaitForReady(t, 90*time.Second)
	return c
}

func (m *mailpitContainer) WaitForReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := m.apiURL + "/api/v1/info"
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	logs, _ := exec.Command("podman", "logs", m.name).CombinedOutput()
	t.Fatalf("mailpit did not become ready within %s. container logs:\n%s", timeout, string(logs))
}

func (m *mailpitContainer) Messages(t *testing.T) []mailpitMessage {
	t.Helper()
	resp, err := http.Get(m.apiURL + "/api/v1/messages")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET /api/v1/messages")
	var page mailpitMessagesPage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	return page.Messages
}

func (m *mailpitContainer) WaitForMessages(t *testing.T, atLeast int, timeout time.Duration) []mailpitMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []mailpitMessage
	for time.Now().Before(deadline) {
		last = m.Messages(t)
		if len(last) >= atLeast {
			return last
		}
		time.Sleep(150 * time.Millisecond)
	}
	logs, _ := exec.Command("podman", "logs", m.name).CombinedOutput()
	t.Fatalf("mailpit never received %d message(s) within %s (got %d). container logs:\n%s",
		atLeast, timeout, len(last), string(logs))
	return nil
}

func (m *mailpitContainer) MessageRaw(t *testing.T, id string) string {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/message/%s/raw", m.apiURL, id))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// relaxedTempDir mirrors t.TempDir() but loosens perms so the rootless-podman
// container user (a high mapped UID inside a user namespace) can read the
// files we drop into it. t.TempDir() defaults to 0o700 — sufficient for the
// host user but not for the in-container UID.
func relaxedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o755))
	return dir
}

// generateSelfSignedTLS writes an ECDSA P-256 self-signed cert + key into dir.
// The daemon side sets tls_skip_verify=true, so the cert is never validated —
// it just needs to be a real TLS handshake-capable pair.
func generateSelfSignedTLS(t *testing.T, dir string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "runwisp-mailpit-test"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o644))

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0o644))
}

// writeMailpitAuthFile drops an htpasswd-style file into dir/passwd with a
// single bcrypt-hashed entry. Mailpit's MP_SMTP_AUTH_FILE accepts either
// $2a$ or $2y$ prefixes, so we use whatever golang.org/x/crypto/bcrypt emits.
func writeMailpitAuthFile(t *testing.T, dir, user, pass string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.MinCost)
	require.NoError(t, err)
	line := fmt.Sprintf("%s:%s\n", user, string(hash))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "passwd"), []byte(line), 0o644))
}
