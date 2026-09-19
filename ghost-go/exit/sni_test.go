package exit

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// captureClientHello performs a TLS handshake against a pipe and returns the
// bytes the client first wrote (which contain the ClientHello). It never binds
// a socket, so it triggers no firewall prompt.
func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	go func() {
		cfg := &tls.Config{ServerName: serverName, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
		_ = tls.Client(clientSide, cfg).HandshakeContext(context.Background())
	}()

	_ = serverSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := serverSide.Read(buf)
	if err != nil {
		t.Fatalf("read client hello: %v", err)
	}
	return buf[:n]
}

func TestSniffClientHelloSNI(t *testing.T) {
	hello := captureClientHello(t, "www.example.com")
	sni, ok := sniffClientHelloSNI(hello)
	if !ok {
		t.Fatal("expected to extract SNI")
	}
	if sni != "www.example.com" {
		t.Fatalf("got SNI %q want www.example.com", sni)
	}
}

func TestSniffClientHelloSNI_NotTLS(t *testing.T) {
	if _, ok := sniffClientHelloSNI([]byte("GET / HTTP/1.1\r\n")); ok {
		t.Fatal("plain HTTP should not yield an SNI")
	}
	if _, ok := sniffClientHelloSNI(nil); ok {
		t.Fatal("nil should not yield an SNI")
	}
	if _, ok := sniffClientHelloSNI([]byte{0x16, 0x03, 0x01}); ok {
		t.Fatal("truncated record should not yield an SNI")
	}
}
