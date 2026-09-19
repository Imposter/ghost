package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"testing"
	"time"
)

func TestIssueCoturnRESTFormat(t *testing.T) {
	now := time.Unix(1_800_000_000, 500_000_000)
	c := NewIssuer("s3cret", 15*time.Minute).Issue("dev_abc", now)

	if want := "1800000900:dev_abc"; c.Username != want {
		t.Fatalf("username %q, want %q", c.Username, want)
	}
	mac := hmac.New(sha1.New, []byte("s3cret"))
	mac.Write([]byte(c.Username))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); c.Password != want {
		t.Fatalf("password %q, want %q", c.Password, want)
	}
	if c.Expires.Unix() != 1_800_000_900 {
		t.Fatalf("expires %v", c.Expires)
	}
	if def := NewIssuer("s", 0).Issue("u", now); def.Expires.Sub(now) < 59*time.Minute {
		t.Fatalf("default ttl should be an hour: %v", def.Expires.Sub(now))
	}
}
