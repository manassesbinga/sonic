package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// TransparentProxy is the core proxy that intercepts TLS connections,
// performs MITM, executes JavaScript edge workers, and forwards traffic.
//
// Modes:
//   - intercept: TLS MITM with JS worker execution
//   - passthrough-all: forward TLS directly without interception
//   - observe: (reserved for future monitoring mode)
type TransparentProxy struct {
	cfg        *config.Config
	mitmEngine *mitm.MITMEngine
	jsEngine   *runtime.JSEngine
	jsMutex    sync.RWMutex
	listener   net.Listener
	shutdown   chan struct{}
	wg         sync.WaitGroup
	telemetry  *telemetry.Telemetry
	metrics    *telemetry.Metrics
}

// NewTransparentProxy creates a new TransparentProxy with the given config,
// MITM engine, and JS engine. Call Start() to begin accepting connections.
func NewTransparentProxy(cfg *config.Config, mitmEngine *mitm.MITMEngine, jsEngine *runtime.JSEngine) *TransparentProxy {
	return &TransparentProxy{
		cfg:        cfg,
		mitmEngine: mitmEngine,
		jsEngine:   jsEngine,
		shutdown:   make(chan struct{}),
		telemetry:  telemetry.GetTelemetry(),
		metrics:    telemetry.GetMetrics(),
	}
}

// SetJSEngine atomically swaps the JS engine (used for hot-reload).
func (p *TransparentProxy) SetJSEngine(engine *runtime.JSEngine) {
	p.jsMutex.Lock()
	p.jsEngine = engine
	p.jsMutex.Unlock()
}

func (p *TransparentProxy) getJSEngine() *runtime.JSEngine {
	p.jsMutex.RLock()
	defer p.jsMutex.RUnlock()
	return p.jsEngine
}

// Start begins listening for TCP connections on the configured port.
// Non-blocking: connections are handled in goroutines.
func (p *TransparentProxy) Start() error {
	addr := fmt.Sprintf(":%d", p.cfg.ListenPort)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("falha ao iniciar listener proxy transparente na porta %d: %w", p.cfg.ListenPort, err)
	}
	p.listener = l
	p.wg.Add(1)

	go p.acceptConnections()

	return nil
}

// Stop gracefully shuts down the proxy, closing the listener and
// waiting for all active connections to finish.
func (p *TransparentProxy) Stop() {
	close(p.shutdown)
	if p.listener != nil {
		p.listener.Close()
	}
	p.wg.Wait()
}

func (p *TransparentProxy) acceptConnections() {
	defer p.wg.Done()

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.shutdown:
				return
			default:
				continue
			}
		}

		p.wg.Add(1)
		go func(c net.Conn) {
			defer p.wg.Done()
			p.handleConnection(c)
		}(conn)
	}
}

func (p *TransparentProxy) handleConnection(conn net.Conn) {
	defer conn.Close()

	ctx := context.Background()
	if p.telemetry != nil {
		ctx, _ = p.telemetry.StartSpan(ctx, "sonic.handle_connection",
			trace.WithAttributes(attribute.String("sonic.local_addr", conn.LocalAddr().String())),
		)
	}

	bufferedConn := NewBufferedConn(conn)

	header, err := bufferedConn.Peek(5)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "peek_failed", "unknown")
		}
		return
	}

	if header[0] != 0x16 {
		return
	}

	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen <= 0 || recordLen > 8192 {
		return
	}

	peekBytes, err := bufferedConn.Peek(recordLen + 5)
	if err != nil {
		return
	}

	domain, err := ExtractSNI(peekBytes)
	if err != nil {
		return
	}

	if p.telemetry != nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("sonic.domain", domain))
	}

	if p.shouldBypass(domain) {
		p.runPassthrough(ctx, bufferedConn, domain)
		return
	}

	p.runIntercept(ctx, bufferedConn, domain)
}

func (p *TransparentProxy) shouldBypass(domain string) bool {
	if p.cfg.Mode == "passthrough-all" {
		return true
	}

	for _, bypassPattern := range p.cfg.BypassDomains {
		if strings.HasPrefix(bypassPattern, "*.") {
			suffix := bypassPattern[1:]
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		}
		if domain == bypassPattern {
			return true
		}
	}
	return false
}

func (p *TransparentProxy) runPassthrough(ctx context.Context, clientConn net.Conn, domain string) {
	start := time.Now()

	if p.telemetry != nil {
		var spanSpan trace.Span
		ctx, spanSpan = p.telemetry.StartSpan(ctx, "sonic.run_passthrough",
			trace.WithAttributes(attribute.String("sonic.domain", domain)),
		)
		defer spanSpan.End()
	}

	serverAddr := fmt.Sprintf("%s:%s", domain, getUpstreamPort())
	serverConn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "passthrough_connect_failed", domain)
		}
		return
	}
	defer serverConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(serverConn, clientConn)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, serverConn)
	}()

	wg.Wait()

	if p.metrics != nil {
		telemetry.RecordRequest(p.metrics, ctx, domain, "passthrough", 0, time.Since(start))
	}
}

func (p *TransparentProxy) runIntercept(ctx context.Context, clientConn net.Conn, domain string) {
	start := time.Now()
	if p.metrics != nil {
		p.metrics.ConnectionsActive.Add(ctx, 1,
			metric.WithAttributes(attribute.String("sonic.domain", domain), attribute.String("sonic.mode", "intercept")),
		)
		defer p.metrics.ConnectionsActive.Add(ctx, -1,
			metric.WithAttributes(attribute.String("sonic.domain", domain), attribute.String("sonic.mode", "intercept")),
		)
	}

	if p.telemetry != nil {
		var spanSpan trace.Span
		ctx, spanSpan = p.telemetry.StartSpan(ctx, "sonic.run_intercept",
			trace.WithAttributes(attribute.String("sonic.domain", domain)),
		)
		defer spanSpan.End()
	}

	clientTLSConn, err := p.mitmEngine.InterceptTermTLS(clientConn, domain)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "mitm_terminate_failed", domain)
		}
		return
	}
	defer clientTLSConn.Close()

	if err := clientTLSConn.Handshake(); err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "tls_handshake_failed", domain)
		}
		return
	}

	serverTLSConn, err := p.mitmEngine.ConnectRealServer(domain+":"+getUpstreamPort(), domain)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "upstream_connect_failed", domain)
		}
		return
	}
	defer serverTLSConn.Close()

	reader := bufio.NewReader(clientTLSConn)
	reqCount := 0
	for {
		reqStart := time.Now()

		req, err := http.ReadRequest(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return
		}
		reqCount++

		jsReq := &runtime.Request{
			Method:  req.Method,
			URL:     req.URL.String(),
			Path:    req.URL.Path,
			Headers: make(map[string]string),
		}
		for k, v := range req.Header {
			if len(v) > 0 {
				jsReq.Headers[k] = v[0]
			}
		}

		if req.Body != nil {
			bodyBytes, _ := io.ReadAll(req.Body)
			jsReq.Body = string(bodyBytes)
			req.Body.Close()
		}

		trafficResult, err := p.getJSEngine().RunOnTraffic(jsReq)
		if err != nil {
			if p.metrics != nil {
				telemetry.RecordError(p.metrics, ctx, "js_onTraffic_failed", domain)
			}
			if p.telemetry != nil {
				trace.SpanFromContext(ctx).RecordError(err)
			}
			if p.cfg.Runtime.Failsafe == "block" {
				resp := &http.Response{
					StatusCode: 500,
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(http.Header),
				}
				_ = resp.Write(clientTLSConn)
				return
			}
			trafficResult = &runtime.TrafficResult{
				IsResponse: false,
				Method:     jsReq.Method,
				URL:        jsReq.URL,
				Path:       jsReq.Path,
				Headers:    jsReq.Headers,
				Body:       jsReq.Body,
			}
		}

		if trafficResult.IsResponse {
			directResp := &http.Response{
				Status:     fmt.Sprintf("%d %s", trafficResult.Status, http.StatusText(trafficResult.Status)),
				StatusCode: trafficResult.Status,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(trafficResult.Body)),
			}
			for k, v := range trafficResult.Headers {
				directResp.Header.Set(k, v)
			}
			_ = directResp.Write(clientTLSConn)

			if p.metrics != nil {
				telemetry.RecordRequest(p.metrics, ctx, domain, "intercept_direct_resp", trafficResult.Status, time.Since(start))
			}
			return
		}

		newReq, err := http.NewRequest(trafficResult.Method, "https://"+domain+trafficResult.Path, strings.NewReader(trafficResult.Body))
		if err != nil {
			return
		}
		for k, v := range trafficResult.Headers {
			newReq.Header.Set(k, v)
		}

		if err := newReq.Write(serverTLSConn); err != nil {
			return
		}

		resp, err := http.ReadResponse(bufio.NewReader(serverTLSConn), newReq)
		if err != nil {
			return
		}

		jsResp := &runtime.Response{
			Status:  resp.StatusCode,
			Headers: make(map[string]string),
		}
		for k, v := range resp.Header {
			if len(v) > 0 {
				jsResp.Headers[k] = v[0]
			}
		}

		if resp.Body != nil {
			bodyBytes, _ := io.ReadAll(resp.Body)
			jsResp.Body = string(bodyBytes)
			resp.Body.Close()
		}

		modJSResp, err := p.getJSEngine().RunOnResponse(jsResp)
		if err != nil {
			if p.metrics != nil {
				telemetry.RecordError(p.metrics, ctx, "js_onResponse_failed", domain)
			}
			modJSResp = jsResp
		}

		newResp := &http.Response{
			Status:     fmt.Sprintf("%d %s", modJSResp.Status, http.StatusText(modJSResp.Status)),
			StatusCode: modJSResp.Status,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(modJSResp.Body)),
		}
		for k, v := range modJSResp.Headers {
			newResp.Header.Set(k, v)
		}

		if err := newResp.Write(clientTLSConn); err != nil {
			return
		}

		if p.metrics != nil {
			telemetry.RecordRequest(p.metrics, ctx, domain, "intercept", modJSResp.Status, time.Since(reqStart))
		}
	}

	if p.metrics != nil && reqCount > 0 {
		telemetry.RecordRequest(p.metrics, ctx, domain, "intercept", 0, time.Since(start))
	}
}

func getUpstreamPort() string {
	if port := os.Getenv("SONIC_TEST_UPSTREAM_PORT"); port != "" {
		return port
	}
	return "443"
}
