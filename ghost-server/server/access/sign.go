package access

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SignatureHeader carries "sha256=<hex HMAC-SHA256(secret, body)>". The body
// includes ts and nonce, so the signature covers them too.
const SignatureHeader = "X-Ghost-Signature"

// AuthorizePath is appended to the authorizer base URL.
const AuthorizePath = "/ghost/authorize"

// Sign returns the SignatureHeader value for body.
func Sign(secret []byte, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// NewNonce returns a random 128-bit hex nonce.
func NewNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("access: crypto/rand: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// Verification errors returned by Verifier.
var (
	ErrBadSignature = errors.New("access: bad signature")
	ErrStale        = errors.New("access: timestamp outside the allowed window")
	ErrReplay       = errors.New("access: nonce already used")
	ErrMalformed    = errors.New("access: malformed request")
)

// Verifier checks signed authorizer requests: the HMAC signature, that ts is
// within MaxSkew of now, and that the nonce has not been seen within the skew
// window. It is what an authorizer implementation (for example scrape_bot's
// API) runs on each request. Safe for concurrent use.
type Verifier struct {
	secret  []byte
	maxSkew time.Duration
	now     func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time // nonce -> expiry
}

// NewVerifier returns a Verifier. maxSkew <= 0 defaults to five minutes; now
// may be nil for time.Now.
func NewVerifier(secret []byte, maxSkew time.Duration, now func() time.Time) *Verifier {
	if maxSkew <= 0 {
		maxSkew = 5 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &Verifier{secret: secret, maxSkew: maxSkew, now: now, nonces: map[string]time.Time{}}
}

// Verify checks body against signature and returns the decoded request.
func (v *Verifier) Verify(body []byte, signature string) (Request, error) {
	var req Request
	want := Sign(v.secret, body)
	if !hmac.Equal([]byte(want), []byte(strings.TrimSpace(signature))) {
		return req, ErrBadSignature
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Nonce == "" || req.TS == 0 || req.Action == "" {
		return req, ErrMalformed
	}
	now := v.now()
	ts := time.Unix(req.TS, 0)
	if ts.Before(now.Add(-v.maxSkew)) || ts.After(now.Add(v.maxSkew)) {
		return req, ErrStale
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	for n, exp := range v.nonces {
		if now.After(exp) {
			delete(v.nonces, n)
		}
	}
	if _, seen := v.nonces[req.Nonce]; seen {
		return req, ErrReplay
	}
	// A nonce only needs remembering until its timestamp leaves the window.
	v.nonces[req.Nonce] = ts.Add(v.maxSkew)
	return req, nil
}

// VerifyHTTP reads and verifies an *http.Request's body and signature.
func (v *Verifier) VerifyHTTP(r *http.Request) (Request, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return Request{}, ErrMalformed
	}
	return v.Verify(body, r.Header.Get(SignatureHeader))
}
