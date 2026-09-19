// Package turn issues short-lived TURN credentials using the coturn REST-API
// shared-secret scheme (coturn's use-auth-secret / static-auth-secret, see
// draft-uberti-behave-turn-rest):
//
//	username = "<expiry unix seconds>:<user id>"
//	password = base64(HMAC-SHA1(secret, username))
//
// coturn recomputes the password from the username and rejects it once the
// expiry has passed, so the server never has to share state with coturn.
package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"time"
)

// Credential is one set of TURN credentials.
type Credential struct {
	Username string
	Password string
	Expires  time.Time
}

// Issuer mints credentials for a fixed secret and lifetime.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

// NewIssuer returns an Issuer. ttl <= 0 defaults to one hour.
func NewIssuer(secret string, ttl time.Duration) *Issuer {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// Issue returns credentials for userID valid until now+ttl.
func (i *Issuer) Issue(userID string, now time.Time) Credential {
	exp := now.Add(i.ttl).Truncate(time.Second)
	username := strconv.FormatInt(exp.Unix(), 10) + ":" + userID
	return Credential{Username: username, Password: Password(i.secret, username), Expires: exp}
}

// Password computes the coturn REST password for username.
func Password(secret []byte, username string) string {
	mac := hmac.New(sha1.New, secret)
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
