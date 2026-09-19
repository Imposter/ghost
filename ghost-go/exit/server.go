package exit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// errCapExhausted is returned internally when the daily byte budget runs out
// mid-transfer.
var errCapExhausted = errors.New("exit: bandwidth cap exhausted")

// Dialer dials a target through the host OS network. It defaults to a
// net.Dialer; tests can override it.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// Config configures an exit Server.
type Config struct {
	// Policy decides which destinations are allowed. Defaults to DenyAll.
	Policy Policy
	// Accountant receives a record for each finished connection. Defaults to a
	// no-op.
	Accountant Accountant
	// Dialer dials targets through the host network. Defaults to net.Dialer.
	Dialer Dialer
	// Logger is optional (defaults to slog.Default()).
	Logger *slog.Logger

	// DailyCapBytes is the per-day byte budget (0 = unlimited).
	DailyCapBytes int64
	// BytesPerSecond rate-limits total throughput (0 = unlimited).
	BytesPerSecond int64

	// DialTimeout bounds a single target dial (default 15s).
	DialTimeout time.Duration
	// IdleTimeout closes idle proxied connections (0 = no idle timeout).
	IdleTimeout time.Duration

	// HTTPSourceHeader names the HTTP-CONNECT header carrying the source tag
	// (default "X-Ghost-Source").
	HTTPSourceHeader string

	// AllowLoopbackForTest, when true, disables the loopback/private
	// destination guard. It exists ONLY for tests that dial a local fake
	// target and must never be set in production.
	AllowLoopbackForTest bool

	// MeterProvider and TracerProvider supply OpenTelemetry instrumentation.
	// Both are optional and default to the global providers
	// (otel.GetMeterProvider / otel.GetTracerProvider). The exit package
	// depends only on the OTel API; the SDK and exporters belong in binaries.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider
}

// Server is a SOCKS5 (CONNECT) and HTTP-CONNECT proxy. It serves listeners
// bound to the node's tunnel IP and dials targets through the host network.
type Server struct {
	cfg  Config
	log  *slog.Logger
	cap  *capController
	dial Dialer

	metrics *exitMetrics

	mu        sync.Mutex
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	active    int
	closed    bool
	wg        sync.WaitGroup
}

// New creates an exit Server from cfg.
func New(cfg Config) *Server {
	if cfg.Policy == nil {
		cfg.Policy = DenyAll{}
	}
	if cfg.Accountant == nil {
		cfg.Accountant = NopAccountant{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 15 * time.Second
	}
	if cfg.HTTPSourceHeader == "" {
		cfg.HTTPSourceHeader = "X-Ghost-Source"
	}
	dial := cfg.Dialer
	if dial == nil {
		dial = &net.Dialer{}
	}
	m, err := newExitMetrics(cfg.MeterProvider, cfg.TracerProvider)
	if err != nil {
		// Instruments only fail on a misbehaving provider; fall back to a nil
		// metrics set (record becomes a no-op) rather than refusing to serve.
		cfg.Logger.Warn("exit: metrics init failed", "error", err)
		m = nil
	}
	return &Server{
		cfg:     cfg,
		log:     cfg.Logger,
		cap:     newCapController(cfg.DailyCapBytes, cfg.BytesPerSecond),
		dial:    dial,
		metrics: m,
		conns:   make(map[net.Conn]struct{}),
	}
}

// Policy returns the server's policy.
func (s *Server) Policy() Policy { return s.cfg.Policy }

// SetPaused pauses or resumes the exit. New connections are refused while
// paused; existing connections continue.
func (s *Server) SetPaused(p bool) { s.cap.SetPaused(p) }

// Paused reports whether the exit is paused.
func (s *Server) Paused() bool { return s.cap.Paused() }

// SetDailyCap updates the per-day byte budget (0 = unlimited).
func (s *Server) SetDailyCap(n int64) { s.cap.SetDailyLimit(n) }

// SetRate updates the bytes-per-second limit (0 = unlimited).
func (s *Server) SetRate(bytesPerSec int64) { s.cap.SetRate(bytesPerSec) }

// UsageToday returns bytes transferred today and the daily limit.
func (s *Server) UsageToday() (used, limit int64) { return s.cap.UsageToday() }

// ActiveConns returns the number of live proxied connections.
func (s *Server) ActiveConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Serve accepts connections on ln until the server is closed. It always
// returns a non-nil error (net.ErrClosed on shutdown).
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.listeners = append(s.listeners, ln)
	s.mu.Unlock()

	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return net.ErrClosed
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(c)
		}()
	}
}

// Close stops the server, closes all listeners, and closes active connections.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	lns := s.listeners
	s.listeners = nil
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, ln := range lns {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) addActive(ctx context.Context, delta int64) {
	if s.metrics != nil {
		s.metrics.active.Add(ctx, delta)
	}
}

func (s *Server) trackConn(c net.Conn, add bool) {
	s.mu.Lock()
	if add {
		s.conns[c] = struct{}{}
		s.active++
	} else {
		delete(s.conns, c)
		s.active--
	}
	s.mu.Unlock()
}

func (s *Server) handle(client net.Conn) {
	s.trackConn(client, true)
	defer s.trackConn(client, false)
	defer client.Close()

	br := bufio.NewReader(client)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		s.handleSOCKS5(client, br)
		return
	}
	s.handleHTTPConnect(client, br)
}

// proxyRequest is the parsed CONNECT request shared by both protocols.
type proxyRequest struct {
	proto     Protocol
	host      string
	port      int
	sourceTag string
}

// dialAndPipe applies the policy + safety guard, dials, and pipes bytes while
// sniffing SNI, tracking bytes, TTFB, and enforcing the cap. It emits an
// accounting record when done. `writeReply` sends the protocol-specific
// success/failure reply to the client before piping.
func (s *Server) handleRequest(client net.Conn, br *bufio.Reader, req proxyRequest,
	replyOK func() error, replyErr func(Result) error) {

	info := ConnInfo{
		Protocol:  req.proto,
		SourceTag: req.sourceTag,
		DestHost:  req.host,
		DestPort:  req.port,
		Start:     time.Now(),
	}
	if ra := client.RemoteAddr(); ra != nil {
		info.SourcePeer = ra.String()
	}

	// One span per exit connection (dial plus copy).
	ctx := context.Background()
	var span trace.Span
	if s.metrics != nil {
		ctx, span = s.metrics.tracer.Start(ctx, "exit.connection", trace.WithSpanKind(trace.SpanKindClient))
	}
	s.addActive(ctx, 1)

	finish := func(res Result, errMsg string) {
		info.Result = res
		info.Err = errMsg
		info.Duration = time.Since(info.Start)
		s.cfg.Accountant.Record(info)
		if s.metrics != nil {
			s.metrics.record(ctx, info)
		}
		s.addActive(ctx, -1)
		if span != nil {
			span.SetAttributes(baseAttrs(info, res == ResultAllowed)...)
			if info.DestIP != "" {
				span.SetAttributes(attribute.String("server.socket.address", info.DestIP))
			}
			span.SetAttributes(attribute.String("result", string(res)))
			if res != ResultAllowed && res != ResultDenied {
				span.SetStatus(codes.Error, errMsg)
			}
			span.End()
		}
	}

	if !s.cap.Allowed() {
		_ = replyErr(ResultCapped)
		finish(ResultCapped, "paused or daily cap reached")
		return
	}

	if req.port <= 0 || req.port > 65535 {
		_ = replyErr(ResultDenied)
		finish(ResultDenied, "invalid port")
		return
	}

	if !s.cfg.Policy.Allow(req.host, req.port) {
		_ = replyErr(ResultDenied)
		finish(ResultDenied, "destination not allowed by policy")
		return
	}

	// Resolve and apply the hard safety guard, unless the policy explicitly
	// named an IP literal that is otherwise forbidden.
	target, resolvedIP, res, errMsg := s.resolve(req.host, req.port)
	if res != ResultAllowed {
		_ = replyErr(res)
		finish(res, errMsg)
		return
	}
	info.DestIP = resolvedIP

	dialCtx, cancel := context.WithTimeout(context.Background(), s.cfg.DialTimeout)
	dialStart := time.Now()
	upstream, err := s.dial.DialContext(dialCtx, "tcp", target)
	cancel()
	if err != nil {
		res := ResultDialError
		if isTimeout(err) {
			res = ResultTimeout
		}
		_ = replyErr(res)
		finish(res, err.Error())
		return
	}
	defer upstream.Close()

	if err := replyOK(); err != nil {
		finish(ResultDialError, "reply: "+err.Error())
		return
	}

	s.pipe(client, br, upstream, &info, dialStart)
	finish(ResultAllowed, "")
}

// resolve resolves host and returns the dial target "ip:port", the chosen IP,
// and a Result. It enforces the forbidden-destination guard for every
// resolved IP unless the policy explicitly allowed that exact IP literal.
func (s *Server) resolve(host string, port int) (target, ip string, res Result, errMsg string) {
	if literal := net.ParseIP(host); literal != nil {
		// The policy already allowed this host:port at the caller. A forbidden
		// literal (loopback/private/etc.) is only reachable here when the
		// policy named that exact IP, which is the "explicitly names them"
		// exception the spec allows.
		return net.JoinHostPort(host, strconv.Itoa(port)), literal.String(), ResultAllowed, ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.DialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		if isTimeout(err) {
			return "", "", ResultTimeout, err.Error()
		}
		return "", "", ResultDialError, err.Error()
	}
	for _, cand := range ips {
		if IsForbiddenDestination(cand) && !s.cfg.AllowLoopbackForTest {
			continue
		}
		return net.JoinHostPort(cand.String(), strconv.Itoa(port)), cand.String(), ResultAllowed, ""
	}
	return "", "", ResultDenied, "all resolved addresses are forbidden"
}

// pipe copies bytes in both directions, sniffing SNI from the first client
// bytes and measuring TTFB and byte counts.
func (s *Server) pipe(client net.Conn, br *bufio.Reader, upstream net.Conn, info *ConnInfo, dialStart time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Sniff SNI from the client's first segment. Peek(1) blocks only until the
	// first byte arrives (the client always speaks first on a CONNECT tunnel),
	// then Buffered() is whatever came in that read — typically the whole
	// ClientHello. We never block waiting for a fixed length.
	if _, err := br.Peek(1); err == nil {
		if peek, err := br.Peek(br.Buffered()); err == nil && len(peek) > 0 {
			if sni, ok := sniffClientHelloSNI(peek); ok {
				info.SNI = sni
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// client -> upstream (bytes out), rate-limited and capped.
	go func() {
		defer wg.Done()
		cr := &capReader{r: br, cap: s.cap, ctx: ctx, counter: &info.BytesOut}
		_, _ = io.Copy(upstream, cr)
		if cw, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = upstream.Close()
		}
	}()

	// upstream -> client (bytes in), TTFB on first byte, rate-limited/capped.
	go func() {
		defer wg.Done()
		firstByteAt := dialStart
		cr := &capReader{r: upstream, cap: s.cap, ctx: ctx, counter: &info.BytesIn, onFirst: func() {
			firstByteAt = time.Now()
			info.TTFB = firstByteAt.Sub(dialStart)
		}}
		_, _ = io.Copy(client, cr)
		if cw, ok := client.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = client.Close()
		}
	}()

	if s.cfg.IdleTimeout > 0 {
		_ = client.SetDeadline(time.Now().Add(s.cfg.IdleTimeout))
		_ = upstream.SetDeadline(time.Now().Add(s.cfg.IdleTimeout))
	}
	wg.Wait()
}

// handleSOCKS5 handles a SOCKS5 CONNECT (no auth or username/password used as
// the source tag). UDP ASSOCIATE and BIND are refused.
func (s *Server) handleSOCKS5(client net.Conn, br *bufio.Reader) {
	sourceTag, err := socks5Handshake(client, br)
	if err != nil {
		s.log.Debug("socks5 handshake failed", "error", err)
		return
	}
	host, port, cmd, err := socks5ReadRequest(br)
	if err != nil {
		_ = socks5Reply(client, socks5RepGeneralFailure)
		return
	}
	if cmd != socks5CmdConnect {
		_ = socks5Reply(client, socks5RepCommandNotSupported)
		s.cfg.Accountant.Record(ConnInfo{
			Protocol: ProtoSOCKS5, SourceTag: sourceTag, DestHost: host, DestPort: port,
			Result: ResultDenied, Err: "only CONNECT supported", Start: time.Now(),
		})
		return
	}

	req := proxyRequest{proto: ProtoSOCKS5, host: host, port: port, sourceTag: sourceTag}
	s.handleRequest(client, br, req,
		func() error { return socks5Reply(client, socks5RepSuccess) },
		func(res Result) error {
			var rep byte = socks5RepGeneralFailure
			switch res {
			case ResultDenied, ResultCapped:
				rep = socks5RepNotAllowed
			case ResultTimeout:
				rep = socks5RepTTLExpired
			case ResultDialError:
				rep = socks5RepHostUnreachable
			}
			return socks5Reply(client, rep)
		},
	)
}

// handleHTTPConnect handles an HTTP CONNECT request. The source tag is read
// from the configured header.
func (s *Server) handleHTTPConnect(client net.Conn, br *bufio.Reader) {
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 3 || strings.ToUpper(parts[0]) != "CONNECT" {
		_, _ = client.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		return
	}
	hostport := parts[1]
	var sourceTag string
	headerName := strings.ToLower(s.cfg.HTTPSourceHeader)
	for {
		hline, err := br.ReadString('\n')
		if err != nil {
			return
		}
		hline = strings.TrimRight(hline, "\r\n")
		if hline == "" {
			break
		}
		if idx := strings.IndexByte(hline, ':'); idx > 0 {
			name := strings.ToLower(strings.TrimSpace(hline[:idx]))
			if name == headerName {
				sourceTag = strings.TrimSpace(hline[idx+1:])
			}
		}
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		_, _ = client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	port, _ := strconv.Atoi(portStr)

	req := proxyRequest{proto: ProtoHTTPConnect, host: host, port: port, sourceTag: sourceTag}
	s.handleRequest(client, br, req,
		func() error {
			_, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			return err
		},
		func(res Result) error {
			status := "502 Bad Gateway"
			switch res {
			case ResultDenied:
				status = "403 Forbidden"
			case ResultCapped:
				status = "429 Too Many Requests"
			case ResultTimeout:
				status = "504 Gateway Timeout"
			}
			_, err := client.Write([]byte("HTTP/1.1 " + status + "\r\n\r\n"))
			return err
		},
	)
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return errors.Is(err, context.DeadlineExceeded)
}
