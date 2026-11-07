package exit

import (
	"net"
	"time"
)

// Protocol identifies the proxy protocol a connection arrived on.
type Protocol string

const (
	// ProtoSOCKS5 is a SOCKS5 CONNECT request.
	ProtoSOCKS5 Protocol = "socks5"
	// ProtoHTTPConnect is an HTTP CONNECT request.
	ProtoHTTPConnect Protocol = "http_connect"
)

// Result is the outcome of an exit connection attempt.
type Result string

const (
	// ResultAllowed means the connection was permitted and dialed.
	ResultAllowed Result = "allowed"
	// ResultDenied means the policy (or safety guard) refused the destination.
	ResultDenied Result = "denied"
	// ResultDialError means the target could not be dialed.
	ResultDialError Result = "dial_error"
	// ResultTimeout means the dial or transfer timed out.
	ResultTimeout Result = "timeout"
	// ResultCapped means a bandwidth cap refused or cut the connection.
	ResultCapped Result = "capped"
	// ResultOpen marks a connection that is still carrying bytes. The exit
	// never finishes a connection with it; a LiveAccountant shows it on the
	// records of the connections it holds open.
	ResultOpen Result = "open"
)

// ConnInfo is the full record for one exit connection. It is passed to an
// Accountant when the connection finishes (and to a LiveAccountant when its
// tunnel opens). metrics.Collector implements Accountant to export these as
// metrics; the exit package itself only produces the record.
type ConnInfo struct {
	// SourcePeer is the tunnel peer that opened the connection: its peer id
	// when the server's PeerResolver knows it, else its tunnel IP (never the
	// ephemeral port, which would be an unbounded label).
	SourcePeer string
	// SourceTag is the raw caller-supplied tag: the SOCKS5 username, or the
	// value of the configured HTTP-CONNECT header (e.g.
	// "source=tesla-ca&job=8812").
	SourceTag string
	// Source and Job are SourceTag parsed with ParseSourceTag. Metrics are
	// labelled by Source only; Job stays in this record (and so in the
	// connections ring) and on the span.
	Source string
	Job    string
	// Protocol is the proxy protocol used.
	Protocol Protocol
	// SNI is the TLS server name sniffed from the ClientHello, if any.
	SNI string
	// DestHost is the requested destination host (name or IP literal).
	DestHost string
	// DestPort is the requested destination port.
	DestPort int
	// DestIP is the resolved IP actually dialed, if any.
	DestIP string
	// PolicyAllowed reports whether the policy permitted the destination. It
	// can be true with a non-allowed Result (a dial error, a timeout, or a
	// forbidden resolved address). Only permitted hosts become metric labels.
	PolicyAllowed bool
	// Result is the outcome.
	Result Result
	// BytesIn is bytes read from the target and written to the client.
	BytesIn int64
	// BytesOut is bytes read from the client and written to the target.
	BytesOut int64
	// Duration is the total connection lifetime.
	Duration time.Duration
	// TTFB is the time from dial start to the first byte from the target.
	TTFB time.Duration
	// Err is a short error description when Result is an error/denied.
	Err string
	// Start is when the connection was accepted.
	Start time.Time
}

// Accountant receives a record for every finished exit connection. It must be
// safe for concurrent use. The default is a no-op.
type Accountant interface {
	Record(ConnInfo)
}

// LiveAccountant is an Accountant that also follows connections while they
// are open. Once a tunnel is established the exit calls Open, reports bytes on
// the returned LiveConn as they move, and ends it with LiveConn.Close instead
// of Record. Connections that never open a tunnel (refused, failed dials) are
// still reported with Record alone.
type LiveAccountant interface {
	Accountant
	// Open starts following a connection whose tunnel is established. The
	// record has no Result, bytes or Duration yet.
	Open(ConnInfo) LiveConn
}

// LiveConn is one open connection followed by a LiveAccountant. AddBytes is
// called concurrently from the connection's two copy directions.
type LiveConn interface {
	// AddBytes counts bytes just read from the target (in) and from the
	// client (out).
	AddBytes(in, out int64)
	// Close ends the connection with its final record, exactly once, after
	// the last AddBytes. The record's BytesIn and BytesOut equal the sums
	// passed to AddBytes.
	Close(ConnInfo)
}

// NopAccountant discards all records.
type NopAccountant struct{}

// Record does nothing.
func (NopAccountant) Record(ConnInfo) {}

// AccountantFunc adapts a function to the Accountant interface.
type AccountantFunc func(ConnInfo)

// Record calls f.
func (f AccountantFunc) Record(ci ConnInfo) { f(ci) }

// PeerResolver maps a client's tunnel address to the peer id of the peer
// that owns it. ghost.Node and ghost.Hub implement it over their live links.
type PeerResolver interface {
	// PeerForAddr returns the peer id owning addr, or "" when unknown.
	PeerForAddr(addr net.Addr) string
}

// sourcePeer resolves the source peer for a client address: the resolver's
// peer id when known, else the bare IP.
func sourcePeer(r PeerResolver, addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if r != nil {
		if id := r.PeerForAddr(addr); id != "" {
			return id
		}
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host
	}
	return addr.String()
}
