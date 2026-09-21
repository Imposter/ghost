package main

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
)

// A host that keeps the WireGuard key in its own secret store hands libghost
// the private key itself, and a host whose control plane sits behind a
// private CA hands it the authority. Neither needs a file on disk.

// privateKey is a fresh base64 WireGuard private key and its public half.
func privateKey(t *testing.T) (string, string) {
	t.Helper()
	priv, err := wireguard.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, err := ghost.KeysFromPrivateKey(wireguard.EncodeKey(priv))
	if err != nil {
		t.Fatal(err)
	}
	return wireguard.EncodeKey(priv), k.PublicKey()
}

// enrollServer answers every enrolment and records the body it was sent.
func enrollServer(t *testing.T, tlsServer bool) (*httptest.Server, *enrollBody) {
	t.Helper()
	var got enrollBody
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"peer_id":"peer_1","peer_token":"gpt_x","network":"lab"}`)
	})
	var srv *httptest.Server
	if tlsServer {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, &got
}

func enrollJSON(t *testing.T, req enrollRequestJSON) string {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnrollWithPrivateKey(t *testing.T) {
	srv, got := enrollServer(t, false)
	priv, pub := privateKey(t)

	res := decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", PrivateKey: priv,
	})))
	if !res.OK {
		t.Fatalf("enroll: %+v", res)
	}
	if got.PublicKey != pub || res.Creds.PublicKey != pub {
		t.Errorf("registered %q (creds %q), want the public half %q", got.PublicKey, res.Creds.PublicKey, pub)
	}

	// The matching public key alongside is fine; a different one is refused
	// before anything is sent.
	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", PrivateKey: priv, PublicKey: pub,
	})))
	if !res.OK {
		t.Errorf("matching pair refused: %+v", res)
	}
	_, other := privateKey(t)
	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", PrivateKey: priv, PublicKey: other,
	})))
	if res.OK || !strings.Contains(res.Error, "not the public half") {
		t.Errorf("mismatched pair: %+v", res)
	}

	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", PrivateKey: priv,
		KeyStorePath: filepath.Join(t.TempDir(), "keys.json"),
	})))
	if res.OK || !strings.Contains(res.Error, "not both") {
		t.Errorf("key and key store: %+v", res)
	}

	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", PrivateKey: "not-a-key",
	})))
	if res.OK || !strings.Contains(res.Error, "private key") {
		t.Errorf("malformed key: %+v", res)
	}
}

func TestEnrollTrustsGivenCA(t *testing.T) {
	srv, _ := enrollServer(t, true)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))

	res := decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good",
	})))
	if res.OK || !strings.Contains(res.Error, "certificate") {
		t.Fatalf("an unknown authority was trusted: %+v", res)
	}

	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", CACertPEM: ca,
	})))
	if !res.OK {
		t.Fatalf("enroll with the CA: %+v", res)
	}

	res = decode[enrollResponse](t, apiEnroll(enrollJSON(t, enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", CACertPEM: "not pem",
	})))
	if res.OK || !strings.Contains(res.Error, "no PEM certificate") {
		t.Errorf("junk CA: %+v", res)
	}
}

func TestStartConfigKeyAndCA(t *testing.T) {
	priv, _ := privateKey(t)
	srv, _ := enrollServer(t, true)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	creds := credsConfig{Server: "https://x", PeerToken: "t", Network: "n"}

	cfg, _, err := (&startConfig{Creds: creds, PrivateKey: priv, CACertPEM: ca}).parse()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrivateKey != priv || cfg.KeyStorePath != "" {
		t.Errorf("keys: private %t, store %q", cfg.PrivateKey == priv, cfg.KeyStorePath)
	}
	if cfg.SignalTLS == nil || cfg.SignalTLS.RootCAs == nil {
		t.Error("the CA did not reach the signalling TLS config")
	}

	cfg, _, err = (&startConfig{Creds: creds}).parse()
	if err != nil || cfg.SignalTLS != nil {
		t.Errorf("no CA: tls %v, err %v", cfg.SignalTLS, err)
	}

	for name, sc := range map[string]startConfig{
		"not both": {Creds: creds, PrivateKey: priv, KeyStorePath: "k.json"},
		"no PEM":   {Creds: creds, CACertPEM: "junk"},
	} {
		if _, _, err := sc.parse(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The JSON a host sends decodes strictly into those fields.
	var sc startConfig
	doc := `{"creds":{"server":"https://x","peer_token":"t","network":"n"},"private_key":"` + priv + `","ca_cert_pem":"x"}`
	if err := decodeJSON(doc, &sc); err != nil || sc.PrivateKey != priv || sc.CACertPEM != "x" {
		t.Errorf("decode: %v %+v", err, sc)
	}
}
