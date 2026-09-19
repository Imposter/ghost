package exit

import (
	"strings"
	"testing"
)

func TestParseSourceTag(t *testing.T) {
	tests := []struct {
		raw  string
		want SourceTag
	}{
		{"", SourceTag{}},
		{"   ", SourceTag{}},
		{"tesla-ca", SourceTag{Source: "tesla-ca"}},
		{"source=tesla-ca", SourceTag{Source: "tesla-ca"}},
		{"source=tesla-ca&job=8812", SourceTag{Source: "tesla-ca", Job: "8812"}},
		{"job=8812&source=tesla-ca", SourceTag{Source: "tesla-ca", Job: "8812"}},
		{"job=42", SourceTag{Job: "42"}},
		{"source=a%26b&job=x%3Dy", SourceTag{Source: "a&b", Job: "x=y"}},
		{"source=bad%zz", SourceTag{Source: "bad%zz"}},
		{"source=first&source=second", SourceTag{Source: "first"}},
		{"source=s&run=7&job=j", SourceTag{Source: "s", Job: "j"}},
		{"garbage&source=s", SourceTag{Source: "s"}},
		{" source = s & job = j ", SourceTag{Source: "s", Job: "j"}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := ParseSourceTag(tt.raw); got != tt.want {
				t.Errorf("ParseSourceTag(%q)=%+v want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSourceTagRoundTrip(t *testing.T) {
	for _, st := range []SourceTag{
		{},
		{Source: "tesla-ca"},
		{Job: "8812"},
		{Source: "tesla-ca", Job: "8812"},
		{Source: "a&b c", Job: "x=y/z"},
	} {
		enc := st.String()
		if got := ParseSourceTag(enc); got != st {
			t.Errorf("%+v -> %q -> %+v", st, enc, got)
		}
	}
	if got := (SourceTag{Source: "s", Job: "j"}).String(); got != "source=s&job=j" {
		t.Errorf("canonical form=%q", got)
	}
}

func TestSourceLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"tesla-ca", "tesla-ca"},
		{"feed.v2_main:eu/west", "feed.v2_main:eu/west"},
		{"has space", OverflowSource},
		{"a&b", OverflowSource},
		{"café", OverflowSource},
		{strings.Repeat("x", MaxSourceLen), strings.Repeat("x", MaxSourceLen)},
		{strings.Repeat("x", MaxSourceLen+1), OverflowSource},
	}
	for _, tt := range tests {
		if got := SourceLabel(tt.in); got != tt.want {
			t.Errorf("SourceLabel(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}
