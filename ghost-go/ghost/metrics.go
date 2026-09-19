package ghost

import (
	"context"
	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"encoding/hex"

	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
)

const meterName = "github.com/Imposter/ghost/ghost-go/ghost"
const meterVersion = "0.1.0"

// tunnelMetrics registers OpenTelemetry async gauges reporting per-peer tunnel
// statistics: ICE round-trip time, WireGuard handshake age, and rx/tx bytes,
// each tagged with the peer and the ICE candidate type. The gauges observe the
// mesh's live links on each collection, so no work happens unless a reader
// collects.
type tunnelMetrics struct {
	reg metric.Registration
}

func (m *mesh) registerMetrics() error {
	mp := m.cfg.MeterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(meterName, metric.WithInstrumentationVersion(meterVersion))

	rtt, err := meter.Float64ObservableGauge("ghost.tunnel.rtt",
		metric.WithDescription("ICE round-trip time to a peer"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}
	handshakeAge, err := meter.Float64ObservableGauge("ghost.tunnel.handshake_age",
		metric.WithDescription("Age of the last WireGuard handshake with a peer"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}
	rx, err := meter.Int64ObservableCounter("ghost.tunnel.rx_bytes",
		metric.WithDescription("Bytes received from a peer over the tunnel transport"),
		metric.WithUnit("By"))
	if err != nil {
		return err
	}
	tx, err := meter.Int64ObservableCounter("ghost.tunnel.tx_bytes",
		metric.WithDescription("Bytes sent to a peer over the tunnel transport"),
		metric.WithUnit("By"))
	if err != nil {
		return err
	}
	peers, err := meter.Int64ObservableGauge("ghost.tunnel.peers",
		metric.WithDescription("Connected tunnel peers"),
		metric.WithUnit("{peer}"))
	if err != nil {
		return err
	}

	cb := func(_ context.Context, o metric.Observer) error {
		snap := m.linkSnapshot()
		connected := int64(0)
		for _, l := range snap {
			if !l.added {
				continue
			}
			connected++
			attrs := metric.WithAttributes(
				attribute.String("ghost.peer.id", l.peerID),
				attribute.String("ice.candidate.type", l.candType),
			)
			if l.rttSeconds > 0 {
				o.ObserveFloat64(rtt, l.rttSeconds, attrs)
			}
			if l.handshakeAgeSeconds >= 0 {
				o.ObserveFloat64(handshakeAge, l.handshakeAgeSeconds, attrs)
			}
			o.ObserveInt64(rx, int64(l.rx), attrs)
			o.ObserveInt64(tx, int64(l.tx), attrs)
		}
		o.ObserveInt64(peers, connected)
		return nil
	}

	reg, err := meter.RegisterCallback(cb, rtt, handshakeAge, rx, tx, peers)
	if err != nil {
		return err
	}
	m.tmetrics = &tunnelMetrics{reg: reg}
	return nil
}

// linkStat is a per-peer metric snapshot.
type linkStat struct {
	peerID              string
	address             string
	candType            string
	added               bool
	rttSeconds          float64
	handshakeAgeSeconds float64
	lastHandshake       time.Time
	rx                  uint64
	tx                  uint64
}

// linkSnapshot gathers current stats for every link without holding locks
// during observation callbacks longer than necessary.
// linkFields is the part of a peerLink the metrics snapshot reads, copied under the
// mesh lock so a concurrent connect never races the scrape.
type linkFields struct {
	peerID, publicKey, address, epKey, candType string
	added                                       bool
	agent                                       ice.Agent
}

func (m *mesh) linkSnapshot() []linkStat {
	// Copy each link's fields under the lock: the connect goroutine sets candType and
	// conn under the same lock once ICE settles.
	m.mu.Lock()
	links := make([]linkFields, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, linkFields{
			peerID: l.peerID, address: l.address, candType: l.candType, added: l.added,
			agent: l.agent, epKey: l.epKey, publicKey: l.publicKey,
		})
	}
	bind := m.bind
	dev := m.wg
	m.mu.Unlock()

	handshakes := map[string]time.Time{}
	if dev != nil {
		if status, err := dev.GetStatus(); err == nil {
			handshakes = parseHandshakeTimes(status)
		}
	}

	out := make([]linkStat, 0, len(links))
	for _, l := range links {
		st := linkStat{
			peerID:              l.peerID,
			address:             l.address,
			candType:            l.candType,
			added:               l.added,
			handshakeAgeSeconds: -1,
		}
		if l.agent != nil {
			st.rttSeconds = l.agent.RoundTripTime().Seconds()
		}
		if bind != nil {
			st.rx, st.tx = bind.Counters(l.epKey)
		}
		if t, ok := handshakes[l.publicKey]; ok && !t.IsZero() {
			st.lastHandshake = t
			st.handshakeAgeSeconds = time.Since(t).Seconds()
		}
		out = append(out, st)
	}
	return out
}

// parseHandshakeTimes parses a wireguard-go IpcGet status blob into a map from
// base64 public key to the last handshake time.
func parseHandshakeTimes(status string) map[string]time.Time {
	out := map[string]time.Time{}
	var curKey string
	var sec int64
	flush := func() {
		if curKey != "" && sec > 0 {
			if raw, err := hex.DecodeString(curKey); err == nil {
				out[wireguard.EncodeKey(raw)] = time.Unix(sec, 0)
			}
		}
		sec = 0
	}
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			curKey = v
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	flush()
	return out
}
