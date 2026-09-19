package direct

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func sampleToken() Token {
	return Token{
		V:          TokenVersion,
		Kind:       KindInvite,
		Session:    "abc_DEF-123",
		PublicKey:  base64.StdEncoding.EncodeToString(make([]byte, 32)),
		Address:    "100.64.0.1/32",
		AllowedIPs: []string{"100.64.0.1/32"},
		Ufrag:      "ufragufrag",
		Pwd:        "passwordpasswordpassword",
		Candidates: []proto.Candidate{{Type: "host", Address: "127.0.0.1", Port: 50000, Protocol: "udp", Priority: 1, Foundation: "1"}},
		Expires:    time.Now().Add(time.Hour).Unix(),
	}
}

func encodeRaw(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestTokenRoundTrip(t *testing.T) {
	in := sampleToken()
	s, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseToken(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the token:\n in %+v\nout %+v", in, out)
	}
}

func TestParseTokenRejects(t *testing.T) {
	mut := func(f func(*Token)) string {
		tok := sampleToken()
		f(&tok)
		b, _ := json.Marshal(tok)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	withField := func(k string, v any) string {
		var m map[string]any
		b, _ := json.Marshal(sampleToken())
		_ = json.Unmarshal(b, &m)
		m[k] = v
		return encodeRaw(t, m)
	}
	cases := []struct {
		name string
		tok  string
		want error
	}{
		{"bad version", mut(func(t *Token) { t.V = 2 }), ErrTokenVersion},
		{"expired", mut(func(t *Token) { t.Expires = time.Now().Add(-time.Minute).Unix() }), ErrTokenExpired},
		{"oversized", strings.Repeat("A", MaxTokenSize+1), ErrTokenSize},
		{"private key", withField("private_key", base64.StdEncoding.EncodeToString(make([]byte, 32))), ErrPrivateKey},
		{"unknown field", withField("extra", 1), ErrTokenInvalid},
		{"not base64", "!!!", ErrTokenInvalid},
		{"bad kind", mut(func(t *Token) { t.Kind = "other" }), ErrTokenInvalid},
		{"bad key", mut(func(t *Token) { t.PublicKey = "short" }), ErrTokenInvalid},
		{"subnet address", mut(func(t *Token) { t.Address = "100.64.0.0/10" }), ErrTokenInvalid},
		{"foreign allowed ip", mut(func(t *Token) { t.AllowedIPs = []string{"100.64.0.0/10"} }), ErrTokenInvalid},
		{"no ice creds", mut(func(t *Token) { t.Pwd = "" }), ErrTokenInvalid},
		{"too many candidates", mut(func(t *Token) {
			t.Candidates = make([]proto.Candidate, MaxCandidates+1)
			for i := range t.Candidates {
				t.Candidates[i] = t.Candidates[0]
			}
		}), ErrTokenInvalid},
		{"bad candidate", mut(func(t *Token) { t.Candidates[0].Port = 0 }), ErrTokenInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseToken(c.tok); !errors.Is(err, c.want) {
				t.Fatalf("ParseToken = %v, want %v", err, c.want)
			}
		})
	}
}

func TestTokenWithoutExpiry(t *testing.T) {
	tok := sampleToken()
	tok.Expires = 0
	s, _ := tok.Encode()
	if _, err := parseToken(s, time.Now().Add(100*365*24*time.Hour)); err != nil {
		t.Fatalf("a token without expiry was refused: %v", err)
	}
}
