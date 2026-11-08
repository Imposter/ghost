//go:build android && cgo

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// These tests run on an Android device or emulator only (see docs/ffi.md,
// "Testing on Android"). The ones that reach the internet run only when
// GHOST_TEST_ONLINE=1, so the suite stays offline by default.

// onlineHost is the public HTTPS host the online tests resolve and handshake
// with. Any host with a publicly trusted certificate would do.
const onlineHost = "example.com"

func requireOnline(t *testing.T) {
	t.Helper()
	if os.Getenv("GHOST_TEST_ONLINE") != "1" {
		t.Skip("set GHOST_TEST_ONLINE=1 to reach " + onlineHost)
	}
}

// logcat returns what logcat holds for tag.
func logcat(t *testing.T, tag string) string {
	t.Helper()
	out, err := exec.Command("logcat", "-d", "-s", tag+":V").CombinedOutput()
	require.NoError(t, err, "logcat: %s", out)
	return string(out)
}

func TestAndroidLogsGoToLogcat(t *testing.T) {
	marker := fmt.Sprintf("libghost-android-test-%d", time.Now().UnixNano())
	log, err := newLogger(logOutput, "info")
	require.NoError(t, err)
	log.Warn(marker, "k", "v")

	require.Eventually(t, func() bool {
		for line := range strings.Lines(logcat(t, logTag)) {
			if strings.Contains(line, marker) && strings.Contains(line, " W "+logTag) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "no warning %q in logcat", marker)
}

// androidPanicBystander is a goroutine that is not the one panicking, so a
// traceback that names it shows every goroutine.
func androidPanicBystander(block chan struct{}) { <-block }

func TestAndroidPanicTracesEveryGoroutine(t *testing.T) {
	if marker := os.Getenv("GHOST_TEST_PANIC"); marker != "" {
		go androidPanicBystander(make(chan struct{}))
		time.Sleep(100 * time.Millisecond)
		panic(marker)
	}
	marker := fmt.Sprintf("libghost-android-panic-%d", time.Now().UnixNano())
	cmd := exec.Command(os.Args[0], "-test.run=^TestAndroidPanicTracesEveryGoroutine$")
	cmd.Env = append(os.Environ(), "GHOST_TEST_PANIC="+marker)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "the child should have crashed")
	require.Contains(t, string(out), "androidPanicBystander", "stderr traceback")

	// The runtime writes its crash report to logcat as well, under "Go".
	require.Eventually(t, func() bool {
		l := logcat(t, "Go")
		return strings.Contains(l, marker) && strings.Contains(l, "androidPanicBystander")
	}, 5*time.Second, 100*time.Millisecond, "no full traceback for %q in logcat", marker)
}

func TestAndroidResolves(t *testing.T) {
	requireOnline(t)
	_, err := os.Stat("/etc/resolv.conf")
	t.Logf("/etc/resolv.conf: %v", err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, onlineHost)
	require.NoError(t, err)
	require.NotEmpty(t, addrs)
	t.Logf("%s: %v", onlineHost, addrs)
}

// TestAndroidEnrollReachesServer posts an enrolment the way ghost_enroll does:
// the host has no enrolment endpoint, so a refusal from it proves the name
// resolved and its certificate verified against Android's system roots.
func TestAndroidEnrollReachesServer(t *testing.T) {
	requireOnline(t)
	var resp enrollResponse
	require.NoError(t, json.Unmarshal([]byte(apiEnroll(
		`{"server":"https://`+onlineHost+`","auth_key":"gak_android_test"}`)), &resp))
	t.Logf("enroll: %s", resp.Error)
	require.False(t, resp.OK)
	// A transport failure reads `enroll: Post "https://…": …`.
	require.NotContains(t, resp.Error, "Post ", "the request never got an answer")
}

// TestAndroidSignalTLS opens a signalling socket the way a node does, with the
// default TLS config and with a private CA added to the system roots: both
// must verify the host's public certificate. The host does not speak the
// protocol, so the upgrade itself is refused.
func TestAndroidSignalTLS(t *testing.T) {
	requireOnline(t)
	pool, err := x509.SystemCertPool()
	require.NoError(t, err)
	require.False(t, pool.Equal(x509.NewCertPool()), "no system roots")

	withCA, err := tlsWithCA(selfSignedPEM(t))
	require.NoError(t, err)
	for name, cfg := range map[string]*tls.Config{"system roots": nil, "system roots + ca_cert_pem": withCA} {
		t.Run(name, func(t *testing.T) {
			var opts *websocket.DialOptions
			if cfg != nil {
				opts = &websocket.DialOptions{HTTPClient: httpClientWithTLS(cfg)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ws, resp, err := websocket.Dial(ctx, "wss://"+onlineHost+"/v1/signal", opts)
			if ws != nil {
				_ = ws.CloseNow()
			}
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			require.Error(t, err)
			t.Logf("dial: %v", err)
			require.NotNil(t, resp, "no HTTP response: %v", err)
		})
	}
}

// httpClientWithTLS is the client signal's dialer builds for a TLS config.
func httpClientWithTLS(cfg *tls.Config) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	return &http.Client{Transport: tr}
}

// selfSignedPEM is a throwaway CA certificate, PEM encoded.
func selfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
