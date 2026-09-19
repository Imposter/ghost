package bounded

import (
	"fmt"
	"testing"
)

func TestSetFoldsOverflow(t *testing.T) {
	s := NewSet(2)
	for _, v := range []string{"a", "b", "a"} {
		if got := s.Admit(v); got != v {
			t.Errorf("Admit(%q)=%q", v, got)
		}
	}
	if got := s.Admit("c"); got != Overflow {
		t.Errorf("Admit(c)=%q want %q", got, Overflow)
	}
	if got := s.Admit(""); got != "" {
		t.Errorf("empty passes through, got %q", got)
	}
}

func TestTopNIsCappedAndKeepsHeavyHitters(t *testing.T) {
	tn := NewTopN(3)
	for range 50 {
		tn.Add("heavy")
	}
	for i := range 100 {
		tn.Add(fmt.Sprintf("once-%d", i))
	}
	if tn.Len() != 3 {
		t.Fatalf("len=%d want 3", tn.Len())
	}
	top := tn.Top()
	if top[0].Key != "heavy" || top[0].Count != 50 {
		t.Errorf("top=%+v want heavy=50 first", top[0])
	}
}
