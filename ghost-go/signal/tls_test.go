package signal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A control plane behind a private CA: the default dialer refuses it until it
// is given the authority, then opens the WebSocket.
func TestDefaultDialerTrustsGivenRoots(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()
	url := "wss" + strings.TrimPrefix(srv.URL, "https") + "/v1/signal"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := (defaultDialer{}).Dial(ctx, url); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("an unknown authority was trusted: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	conn, err := (defaultDialer{tls: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}).Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial with the CA: %v", err)
	}
	_ = conn.Close()
}
