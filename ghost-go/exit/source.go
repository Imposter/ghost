package exit

import (
	"net/url"
	"strings"

	"github.com/Imposter/ghost/ghost-go/internal/bounded"
)

// Source tag keys. A structured source tag is a query-string style list of
// key=value pairs joined by "&", for example "source=tesla-ca&job=8812".
const (
	// SourceKey names the tag's source part: a low-cardinality name for the
	// system or feed the connection is for. Metrics are labelled by it.
	SourceKey = "source"
	// JobKey names the tag's job part: a high-cardinality id (one fetch, one
	// run). It is kept in the connection record and on the span only.
	JobKey = "job"
)

// MaxSourceLen is the longest source name used as a metric label. Longer
// names are labelled OverflowSource.
const MaxSourceLen = 64

// OverflowSource is the label value for a source name that is not a valid
// label, and for every name after the first Config.MaxSources distinct ones.
const OverflowSource = bounded.Overflow

// SourceTag is the structured form of the tag a client sends with a request:
// the SOCKS5 username, or the value of the HTTP-CONNECT source header.
type SourceTag struct {
	// Source is the low-cardinality source name (the metric label).
	Source string
	// Job is the per-request id, never a metric label.
	Job string
}

// ParseSourceTag parses a raw source tag.
//
// The canonical form is "source=<s>&job=<j>": "&"-separated key=value pairs
// whose values may be URL query escaped. Keys other than source and job are
// ignored, and the first occurrence of a key wins. A tag with no "=" at all is
// a bare source name, so "tesla-ca" means "source=tesla-ca". Parsing never
// fails; the raw tag is kept on the connection record regardless.
func ParseSourceTag(raw string) SourceTag {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceTag{}
	}
	if !strings.Contains(raw, "=") {
		return SourceTag{Source: raw}
	}
	var t SourceTag
	var haveSource, haveJob bool
	for _, pair := range strings.Split(raw, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if u, err := url.QueryUnescape(v); err == nil {
			v = u
		}
		switch strings.TrimSpace(k) {
		case SourceKey:
			if !haveSource {
				t.Source, haveSource = strings.TrimSpace(v), true
			}
		case JobKey:
			if !haveJob {
				t.Job, haveJob = strings.TrimSpace(v), true
			}
		}
	}
	return t
}

// String encodes t in the canonical form, "source=<s>&job=<j>", omitting
// empty parts and escaping the values. ParseSourceTag(t.String()) == t.
func (t SourceTag) String() string {
	var parts []string
	if t.Source != "" {
		parts = append(parts, SourceKey+"="+url.QueryEscape(t.Source))
	}
	if t.Job != "" {
		parts = append(parts, JobKey+"="+url.QueryEscape(t.Job))
	}
	return strings.Join(parts, "&")
}

// SourceLabel returns the metric label value for a source name: the name
// itself when it is a valid label (1 to MaxSourceLen bytes of ASCII letters,
// digits and ._:/-), "" for an empty name, and OverflowSource otherwise. It
// does not apply the distinct-value bound; the exit and the metrics
// Collector apply that on top.
func SourceLabel(source string) string {
	if source == "" {
		return ""
	}
	if len(source) > MaxSourceLen {
		return OverflowSource
	}
	for i := 0; i < len(source); i++ {
		c := source[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return OverflowSource
		}
	}
	return source
}
