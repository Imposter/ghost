package ipam

import (
	"errors"
	"testing"
)

func TestAllocate(t *testing.T) {
	pool, err := ParsePool("100.64.0.0/10")
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := Allocate(pool, nil); a != "100.64.0.1/32" {
		t.Fatalf("first address %s", a)
	}
	if a, _ := Allocate(pool, []string{"100.64.0.1/32", "100.64.0.2", "garbage"}); a != "100.64.0.3/32" {
		t.Fatalf("next free %s", a)
	}

	small, _ := ParsePool("10.0.0.0/30") // hosts .1 and .2
	if _, err := Allocate(small, []string{"10.0.0.1/32", "10.0.0.2/32"}); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("exhausted: %v", err)
	}
	if !Contains(small, "10.0.0.2/32") || Contains(small, "10.0.0.9/32") {
		t.Fatal("Contains")
	}
	for _, bad := range []string{"fd00::/64", "10.0.0.0/31", "nope"} {
		if _, err := ParsePool(bad); err == nil {
			t.Fatalf("ParsePool(%q) should fail", bad)
		}
	}
}
