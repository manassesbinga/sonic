package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/ebpf"
	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/neuralcache"
	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/security"
	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/proxy"
)

const maxRequestBodySize = 10 * 1024 * 1024 // 10 MB limit for request bodies


// SandboxInterceptor define a interface para interceptar requisições e respostas de tráfego.
type SandboxInterceptor interface {
	InterceptRequest(req *runtime.Request, domain string) (*runtime.Request, *runtime.Response, bool)
	InterceptResponse(resp *runtime.Response, domain string, path string) (*runtime.Response, bool)
}

// TransparentProxy is the core proxy that intercepts TLS connections,
// performs MITM, executes JavaScript edge workers, and forwards traffic.
//
// Modes:
//   - intercept: TLS MITM with JS worker execution
//   - passthrough-all: forward TLS directly without interception
//   - observe: (reserved for future monitoring mode)
type TransparentProxy struct {
	cfgMu                sync.RWMutex
	cfg                  *config.Config
	mitmEngine           *mitm.MITMEngine
	workerEngine         runtime.WorkerEngine
	neuralCache          *neuralcache.NeuralCache
	cveScanner           *security.CVEStreamScanner
	ebpfLoader           *ebpf.EBPFLoader
	jsMutex              sync.RWMutex
	sandboxInterceptor   SandboxInterceptor
	sandboxMu            sync.RWMutex
	listener             net.Listener
	shutdown             chan struct{}
	concurrencySemaphore chan struct{}
	wg                   sync.WaitGroup
	telemetry            *telemetry.Telemetry
	metrics              *telemetry.Metrics
	ctx                  context.Context
	cancel               context.CancelFunc
	activeConns          map[net.Conn]struct{}
	activeConnsMu        sync.Mutex
}

// SetSandboxInterceptor define o interceptador do Sandbox de forma thread-safe.
func (p *TransparentProxy) SetSandboxInterceptor(interceptor SandboxInterceptor) {
	p.sandboxMu.Lock()
	p.sandboxInterceptor = interceptor
	p.sandboxMu.Unlock()
}

// NewTransparentProxy creates a new TransparentProxy with the given config,
// MITM engine, and worker engine. Call Start() to begin accepting connections.
func NewTransparentProxy(cfg *config.Config, mitmEngine *mitm.MITMEngine, workerEngine runtime.WorkerEngine) *TransparentProxy {
	proxyCtx, cancel := context.WithCancel(context.Background())
	nc := neuralcache.NewNeuralCache(1000, 50, telemetry.GetMetrics(), proxyCtx)

	var scanner *security.CVEStreamScanner
	if cfg.Security.Enabled {
		scanner = security.NewCVEStreamScanner(func(event security.SecurityEvent, details map[string]interface{}) {
			if event == security.EventCVEDetected {
				sig, _ := details["signature"].(string)
				prev, _ := details["preview"].(string)
				fmt.Printf("[SECURITY ALERT] CVE signature match detected: %v\n", sig)
				security.AddSecurityIncident(sig, prev)
			}
		})
		scanner.Block = cfg.Security.Block
	}

	return &TransparentProxy{
		cfg:                  cfg,
		mitmEngine:           mitmEngine,
		workerEngine:         workerEngine,
		neuralCache:          nc,
		cveScanner:           scanner,
		ebpfLoader:           ebpf.NewEBPFLoader(),
		ctx:                  proxyCtx,
		shutdown:             make(chan struct{}),
		concurrencySemaphore: make(chan struct{}, 1020),
		telemetry:            telemetry.GetTelemetry(),
		metrics:              telemetry.GetMetrics(),
		cancel:               cancel,
		activeConns:          make(map[net.Conn]struct{}),
	}
}

// Config retorna a instância de configuração de forma thread-safe.
func (p *TransparentProxy) Config() *config.Config {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.cfg
}

// UpdateConfig atualiza a configuração do proxy dinamicamente e de forma thread-safe.
func (p *TransparentProxy) UpdateConfig(cfg *config.Config) {
	p.cfgMu.Lock()
	p.cfg = cfg
	p.cfgMu.Unlock()
}

// CVEScanner retorna a instância de verificação de vulnerabilidades.
func (p *TransparentProxy) CVEScanner() *security.CVEStreamScanner {
	return p.cveScanner
}

// NeuralCache retorna a instância do Neural Cache para acesso externo
// (Usado por JSEngine e outros componentes)
func (p *TransparentProxy) NeuralCache() *neuralcache.NeuralCache {
	return p.neuralCache
}

// SetJSEngine atomically swaps the worker engine (used for hot-reload).
// The caller is responsible for closing the old engine after the swap.
func (p *TransparentProxy) SetJSEngine(engine runtime.WorkerEngine) {
	p.jsMutex.Lock()
	p.workerEngine = engine
	p.jsMutex.Unlock()
}

func (p *TransparentProxy) getProxyCtx() context.Context {
	return p.ctx
}

func (p *TransparentProxy) getWorkerEngine() runtime.WorkerEngine {
	p.jsMutex.RLock()
	defer p.jsMutex.RUnlock()
	return p.workerEngine
}

// Start begins listening for TCP connections on the configured port.
// Non-blocking: connections are handled in goroutines.
func (p *TransparentProxy) Start() error {
	addr := fmt.Sprintf(":%d", p.Config().ListenPort)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("falha ao iniciar listener proxy transparente na porta %d: %w", p.Config().ListenPort, err)
	}
	return p.StartWithListener(l)
}

// StartWithListener starts the proxy using an externally provided listener.
func (p *TransparentProxy) StartWithListener(l net.Listener) error {
	p.listener = l
	p.wg.Add(1)

	if p.ebpfLoader != nil {
		if err := p.ebpfLoader.LoadSockmap(); err != nil {
			fmt.Printf("[WARN] falha ao carregar eBPF sockmap: %v\n", err)
		}
	}

	go p.acceptConnections()

	return nil
}

// Listener returns the active net.Listener of the proxy.
func (p *TransparentProxy) Listener() net.Listener {
	return p.listener
}


func (p *TransparentProxy) registerConn(conn net.Conn) {
	p.activeConnsMu.Lock()
	p.activeConns[conn] = struct{}{}
	p.activeConnsMu.Unlock()
}

func (p *TransparentProxy) unregisterConn(conn net.Conn) {
	p.activeConnsMu.Lock()
	delete(p.activeConns, conn)
	p.activeConnsMu.Unlock()
}

func (p *TransparentProxy) closeActiveConnections() {
	p.activeConnsMu.Lock()
	defer p.activeConnsMu.Unlock()
	for conn := range p.activeConns {
		_ = conn.Close()
	}
}

// Stop gracefully shuts down the proxy, closing the listener and
// waiting for all active connections to finish.
func (p *TransparentProxy) Stop() {
	if p.listener != nil {
		p.listener.Close()
	}
	if p.ebpfLoader != nil {
		p.ebpfLoader.Unload()
	}
	if p.neuralCache != nil {
		p.neuralCache.Close()
	}
	
	close(p.shutdown)

	// Graceful Drain: Espera que todas as conexões ativas terminem
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Todas as conexões terminaram graciosamente
	case <-time.After(10 * time.Second): // 10 segundos de período de drenagem
		// Força o fecho das conexões restantes para não bloquear o encerramento do processo
		p.closeActiveConnections()
		<-done
	}

	if p.cancel != nil {
		p.cancel()
	}
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

		select {
		case p.concurrencySemaphore <- struct{}{}:
			p.wg.Add(1)
			p.registerConn(conn)
			go func(c net.Conn) {
				defer p.wg.Done()
				defer p.unregisterConn(c)
				defer func() { <-p.concurrencySemaphore }()
				p.handleConnection(c)
			}(conn)
		default:
			// Se o pool de conexões simultâneas estiver cheio (Slowloris/DDoS),
			// descarta a conexão imediatamente sem alocar recursos extras.
			_ = conn.Close()
		}
	}
}

func (p *TransparentProxy) handleConnection(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			if p.metrics != nil {
				ctx := p.getProxyCtx()
				telemetry.RecordError(p.metrics, ctx, "panic", "connection_handling")
			}
			fmt.Fprintf(os.Stderr, "[CRITICAL ERROR] Panic detectado no tratamento da conexao: %v\n", r)
		}
		_ = conn.Close()
	}()

	ctx := p.getProxyCtx()
	if p.telemetry != nil {
		var span trace.Span
		ctx, span = p.telemetry.StartSpan(ctx, "sonic.handle_connection",
			trace.WithAttributes(attribute.String("sonic.local_addr", conn.LocalAddr().String())),
		)
		defer span.End()
	}

	bufferedConn := NewBufferedConn(conn)

	// Set a 5-second read deadline for the initial TLS handshake / SNI parsing phase
	// to prevent slowloris/DoS resource exhaustion (idle sockets holding resources).
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	header, err := bufferedConn.Peek(5)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "peek_failed", "unknown")
		}
		return
	}

	if header[0] != 0x16 {
		// Reset read deadline before passing to raw TCP handling
		_ = conn.SetReadDeadline(time.Time{})
		p.handleRawTCP(ctx, bufferedConn)
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

	// Reset read deadline after successful SNI parsing before routing
	_ = conn.SetReadDeadline(time.Time{})

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
	if p.Config().Mode == "passthrough-all" {
		return true
	}

	for _, bypassPattern := range p.Config().BypassDomains {
		if strings.HasPrefix(bypassPattern, "*.") {
			suffix := bypassPattern[1:] // ".stripe.com"
			bareDomain := bypassPattern[2:] // "stripe.com"
			// Corresponde tanto subdomínios (api.stripe.com) como o domínio raiz (stripe.com)
			if strings.HasSuffix(domain, suffix) || domain == bareDomain {
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

	serverAddr := net.JoinHostPort(domain, portForPassthrough(domain, p.Config().TargetPorts))
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	dialer := proxy.FromEnvironment()
	var (
		serverConn net.Conn
		err        error
	)
	if cDialer, ok := dialer.(interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}); ok {
		serverConn, err = cDialer.DialContext(dialCtx, "tcp", serverAddr)
	} else {
		serverConn, err = dialer.Dial("tcp", serverAddr)
	}
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "passthrough_connect_failed", domain)
		}
		return
	}
	defer serverConn.Close()

	// Wrap connections with a 5-minute idle timeout to prevent leaks
	idleTimeout := 5 * time.Minute
	tClientConn := newTimeoutConn(clientConn, idleTimeout)
	tServerConn := newTimeoutConn(serverConn, idleTimeout)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, _ := io.Copy(tServerConn, tClientConn)
		if n > 0 {
			if m := telemetry.GetMetrics(); m != nil {
				telemetry.RecordBytesTransferred(m, ctx, n)
			}
		}
		_ = tServerConn.Close()
		_ = tClientConn.Close()
	}()

	go func() {
		defer wg.Done()
		n, _ := io.Copy(tClientConn, tServerConn)
		if n > 0 {
			if m := telemetry.GetMetrics(); m != nil {
				telemetry.RecordBytesTransferred(m, ctx, n)
			}
		}
		_ = tClientConn.Close()
		_ = tServerConn.Close()
	}()

	wg.Wait()

	if p.metrics != nil {
		telemetry.RecordRequest(p.metrics, ctx, domain, "passthrough", 0, time.Since(start))
	}
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func (p *TransparentProxy) runIntercept(ctx context.Context, clientConn net.Conn, domain string) {
	start := time.Now()
	clientAddr := clientConn.RemoteAddr().String()
	// Extract just the IP address from "IP:port"
	var clientIP string
	if host, _, err := net.SplitHostPort(clientAddr); err == nil {
		clientIP = host
	} else {
		// If splitting fails, use the whole address as fallback
		clientIP = clientAddr
	}
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

	clientTLSConn, err := p.mitmEngine.InterceptTermTLS(ctx, clientConn, domain)
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "mitm_terminate_failed", domain)
		}
		return
	}
	defer clientTLSConn.Close()

	clientTLSConn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := clientTLSConn.Handshake(); err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "tls_handshake_failed", domain)
		}
		return
	}
	clientTLSConn.SetDeadline(time.Time{})

	var serverTLSConn *tls.Conn
	var serverReader *bufio.Reader
	upstreamPort := ""
	defer func() {
		if serverTLSConn != nil {
			serverTLSConn.Close()
		}
	}()

	reader := bufio.NewReader(clientTLSConn)
	reqCount := 0
	for {
		reqStart := time.Now()

		clientTLSConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		req, err := http.ReadRequest(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return
		}
		reqCount++

		jsReq := &runtime.Request{
			Method:     req.Method,
			URL:        req.URL.String(),
			Path:       req.URL.Path,
			Headers:    make(map[string]string),
			ClientAddr: clientIP,
		}
		for k, v := range req.Header {
			if len(v) > 0 {
				jsReq.Headers[k] = v[0]
			}
		}

		var reqBody string
		if req.Body != nil {
			bodyReader := req.Body
			if p.cveScanner != nil {
				bodyReader = security.NewScanningReader(bodyReader, p.cveScanner)
			}
			bodyBytes, readErr := io.ReadAll(io.LimitReader(bodyReader, maxRequestBodySize))
			if readErr != nil {
				req.Body.Close()
				if errors.Is(readErr, security.ErrCVEDetected) {
					if p.metrics != nil {
						telemetry.RecordError(p.metrics, ctx, "cve_blocked", domain)
					}
					resp := &http.Response{
						StatusCode: 403,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("Forbidden by CVE security policy")),
					}
					_ = resp.Write(clientTLSConn)
				}
				return
			}
			reqBody = string(bodyBytes)
			jsReq.Body = reqBody
			req.Body.Close()
		}

		// Interceptação Sandbox (Request Breakpoint)
		p.sandboxMu.RLock()
		interceptor := p.sandboxInterceptor
		p.sandboxMu.RUnlock()
		if interceptor != nil {
			var allowed bool
			var directResp *runtime.Response
			jsReq, directResp, allowed = interceptor.InterceptRequest(jsReq, domain)
			if !allowed {
				resp := &http.Response{
					StatusCode: 403,
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("Forbidden by Sandbox breakpoint policy")),
				}
				_ = resp.Write(clientTLSConn)
				return
			}
			if directResp != nil {
				directRespHTTP := &http.Response{
					Status:     fmt.Sprintf("%d %s", directResp.Status, http.StatusText(directResp.Status)),
					StatusCode: directResp.Status,
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(directResp.Body)),
				}
				for k, v := range directResp.Headers {
					kSanitized := sanitizeHeader(k)
					vSanitized := sanitizeHeader(v)
					if kSanitized == "" {
						continue
					}
					directRespHTTP.Header.Set(kSanitized, vSanitized)
				}
				_ = directRespHTTP.Write(clientTLSConn)
				continue
			}
		}

		// Extrai a porta do Host header para conectar ao upstream correto
		requestPort := extractPortFromHeaders(req.Header)
		if requestPort == "" {
			requestPort = "443"
		}
		if requestPort != upstreamPort {
			if serverTLSConn != nil {
				serverTLSConn.Close()
				serverTLSConn = nil
			}
			newConn, err := p.mitmEngine.ConnectRealServer(ctx, domain+":"+requestPort, domain)
			if err != nil {
				if p.metrics != nil {
					telemetry.RecordError(p.metrics, ctx, "upstream_connect_failed", domain)
				}
				return
			}
			serverTLSConn = newConn
			serverReader = bufio.NewReader(serverTLSConn)
			upstreamPort = requestPort
		}

		packet := &runtime.Packet{
			ID:        fmt.Sprintf("http-req-%d", time.Now().UnixNano()),
			Protocol:  runtime.ProtocolHTTPS, // TLS Interceptado é HTTPS
			Source:    clientAddr,
			Dest:      domain + ":" + upstreamPort,
			IsInbound: true,
			Request:   jsReq,
		}

		engine := p.getWorkerEngine()
		var packetResult *runtime.PacketResult
		if engine != nil {
			var errProcess error
			packetResult, errProcess = engine.ProcessPacket(packet)
			if errProcess != nil {
				if p.metrics != nil {
					telemetry.RecordError(p.metrics, ctx, "worker_onTraffic_failed", domain)
				}
				if p.telemetry != nil {
					trace.SpanFromContext(ctx).RecordError(errProcess)
				}
				if p.Config().Runtime.Failsafe == "block" {
					resp := &http.Response{
						StatusCode: 500,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Header:     make(http.Header),
					}
					_ = resp.Write(clientTLSConn)
					return
				}
				packetResult = &runtime.PacketResult{
					Allow:  true,
					Packet: packet,
				}
			}
		} else {
			packetResult = &runtime.PacketResult{
				Allow:  true,
				Packet: packet,
			}
		}

		if !packetResult.Allow {
			resp := &http.Response{
				StatusCode: 403,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("Forbidden by worker policy")),
			}
			_ = resp.Write(clientTLSConn)
			return
		}

		if packetResult.Response != nil {
			directResp := &http.Response{
				Status:     fmt.Sprintf("%d %s", packetResult.Response.Status, http.StatusText(packetResult.Response.Status)),
				StatusCode: packetResult.Response.Status,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(packetResult.Response.Body)),
			}
			for k, v := range packetResult.Response.Headers {
				kSanitized := sanitizeHeader(k)
				vSanitized := sanitizeHeader(v)
				if kSanitized == "" {
					continue
				}
				directResp.Header.Set(kSanitized, vSanitized)
			}
			if writeErr := directResp.Write(clientTLSConn); writeErr != nil {
				return
			}

			if p.metrics != nil {
				telemetry.RecordRequest(p.metrics, ctx, domain, "intercept_direct_resp", packetResult.Response.Status, time.Since(reqStart))
			}
			return
		}

		if packetResult.Packet == nil {
			packetResult.Packet = packet
		}
		modReq := packetResult.Packet.Request
		if modReq == nil {
			modReq = jsReq
		}

		method := modReq.Method
		if method == "" {
			method = "GET"
		}
		for _, c := range method {
			if c <= 32 || c > 126 {
				method = "GET"
				break
			}
		}

		// Cache key baseado no request MODIFICADO pelo worker
		cacheKey := p.neuralCache.GenerateKey(method, modReq.Path, modReq.Body)
		if cachedEntry, found := p.neuralCache.Get(cacheKey, domain); found {
			directResp := &http.Response{
				Status:     fmt.Sprintf("%d %s", cachedEntry.Status, http.StatusText(cachedEntry.Status)),
				StatusCode: cachedEntry.Status,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(cachedEntry.Value))),
			}
			for k, v := range cachedEntry.Headers {
				if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
					continue
				}
				directResp.Header.Set(k, v)
			}
			if writeErr := directResp.Write(clientTLSConn); writeErr != nil {
				return
			}

			if p.metrics != nil {
				telemetry.RecordRequest(p.metrics, ctx, domain, "intercept_cache_hit", cachedEntry.Status, time.Since(reqStart))
			}
			continue
		}

		for _, c := range modReq.Path {
			if c == '\r' || c == '\n' || (c < 0x20 && c != '\t') {
				return
			}
		}
		newReq, err := http.NewRequest(method, "https://"+domain+modReq.Path, strings.NewReader(modReq.Body))
		if err != nil {
			return
		}
		for k, v := range modReq.Headers {
			kSanitized := sanitizeHeader(k)
			vSanitized := sanitizeHeader(v)
			if kSanitized == "" {
				continue
			}
			newReq.Header.Set(kSanitized, vSanitized)
		}

		// Define deadline de escrita temporário de 15 segundos para enviar a requisição ao upstream
		_ = serverTLSConn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if err := newReq.Write(serverTLSConn); err != nil {
			return
		}
		_ = serverTLSConn.SetWriteDeadline(time.Time{})

		// Define deadline de leitura temporário de 30 segundos para obter a resposta do upstream
		_ = serverTLSConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		resp, err := http.ReadResponse(serverReader, newReq)
		if err != nil {
			return
		}
		_ = serverTLSConn.SetReadDeadline(time.Time{})

		jsResp := &runtime.Response{
			Status:  resp.StatusCode,
			Headers: make(map[string]string),
		}
		for k, v := range resp.Header {
			if len(v) > 0 {
				jsResp.Headers[k] = v[0]
			}
		}

		var respBody string
		if resp.Body != nil {
			bodyReader := resp.Body
			if p.cveScanner != nil {
				bodyReader = security.NewScanningReader(bodyReader, p.cveScanner)
			}
			bodyBytes, readErr := io.ReadAll(io.LimitReader(bodyReader, 10*1024*1024))
			if readErr != nil {
				resp.Body.Close()
				if errors.Is(readErr, security.ErrCVEDetected) {
					if p.metrics != nil {
						telemetry.RecordError(p.metrics, ctx, "cve_response_blocked", domain)
					}
					resp := &http.Response{
						StatusCode: 403,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("Blocked by response CVE security policy")),
					}
					_ = resp.Write(clientTLSConn)
				}
				return
			}
			respBody = string(bodyBytes)
			jsResp.Body = respBody
			resp.Body.Close()
		}

		// Interceptação Sandbox (Response Breakpoint)
		if interceptor != nil {
			var allowed bool
			jsResp, allowed = interceptor.InterceptResponse(jsResp, domain, modReq.Path)
			if !allowed {
				resp := &http.Response{
					StatusCode: 403,
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("Forbidden by Sandbox response breakpoint policy")),
				}
				_ = resp.Write(clientTLSConn)
				return
			}
		}

		respPacket := &runtime.Packet{
			ID:        fmt.Sprintf("http-resp-%d", time.Now().UnixNano()),
			Protocol:  runtime.ProtocolHTTPS,
			Source:    clientAddr,
			Dest:      domain + ":" + upstreamPort,
			IsInbound: false,
			Response:  jsResp,
		}

		var respResult *runtime.PacketResult
		if engine != nil {
			var errProcess error
			respResult, errProcess = engine.ProcessPacket(respPacket)
			if errProcess != nil {
				if p.metrics != nil {
					telemetry.RecordError(p.metrics, ctx, "worker_onResponse_failed", domain)
				}
				respResult = &runtime.PacketResult{
					Allow:  true,
					Packet: respPacket,
				}
			}
		} else {
			respResult = &runtime.PacketResult{
				Allow:  true,
				Packet: respPacket,
			}
		}

		modJSResp := respResult.Packet.Response
		if modJSResp == nil {
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
			kSanitized := sanitizeHeader(k)
			vSanitized := sanitizeHeader(v)
			if kSanitized == "" {
				continue
			}
			newResp.Header.Set(kSanitized, vSanitized)
		}

		// Armazena a resposta no Neural Cache (apenas para requisições GET com method consistente)
		if method == "GET" && modJSResp.Status >= 200 && modJSResp.Status < 300 {
			shouldCache := true
			for k, v := range modJSResp.Headers {
				kLower := strings.ToLower(k)
				if kLower == "set-cookie" {
					shouldCache = false
					break
				}
				if kLower == "cache-control" {
					vLower := strings.ToLower(v)
					if strings.Contains(vLower, "private") || strings.Contains(vLower, "no-store") || strings.Contains(vLower, "no-cache") {
						shouldCache = false
						break
					}
				}
			}
			if shouldCache {
				contentType := resp.Header.Get("Content-Type")
				p.neuralCache.Set(cacheKey, []byte(modJSResp.Body), modJSResp.Headers, modJSResp.Status, contentType)
			}
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

func extractPortFromHeaders(h http.Header) string {
	if host := h.Get("Host"); host != "" {
		if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
			return port
		}
	}
	return ""
}

func portForPassthrough(domain string, targetPorts []int) string {
	if port := os.Getenv("SONIC_TEST_UPSTREAM_PORT"); port != "" {
		return port
	}
	for _, p := range targetPorts {
		if p == 443 {
			return "443"
		}
	}
	if len(targetPorts) > 0 {
		return fmt.Sprintf("%d", targetPorts[0])
	}
	return "443"
}

// handleRawTCP trata tráfego TCP bruto não-TLS (ex: HTTP puro, SSH, SMTP, etc.)
func (p *TransparentProxy) handleRawTCP(ctx context.Context, clientConn *BufferedConn) {
	start := time.Now()
	clientAddr := clientConn.RemoteAddr().String()

	// Define um read deadline curto para verificar se o cliente envia um payload inicial.
	// Isso evita travamento indefinido caso o protocolo seja server-first (ex: SSH, SMTP, FTP).
	rawTimeout := time.Duration(p.Config().Security.RawTCPTimeoutMS) * time.Millisecond
	if rawTimeout <= 0 {
		rawTimeout = 100 * time.Millisecond
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(rawTimeout))
	buf := make([]byte, 4096)
	n, err := clientConn.Read(buf)
	_ = clientConn.SetReadDeadline(time.Time{})

	var netErr net.Error
	isTimeout := false
	if err != nil {
		if errors.As(err, &netErr) && netErr.Timeout() {
			isTimeout = true
			err = nil
		}
	}

	if err != nil && err != io.EOF {
		return
	}

	var payload []byte
	if !isTimeout && n > 0 {
		payload = buf[:n]
	}

	if len(payload) > 0 && p.cveScanner != nil {
		if matched, _ := p.cveScanner.Scan(payload); matched {
			if p.metrics != nil {
				telemetry.RecordError(p.metrics, ctx, "raw_tcp_cve_blocked", clientAddr)
			}
			if p.cveScanner.Block {
				return
			}
		}
	}

	// Cria o Packet genérico com ProtocolRaw
	packet := &runtime.Packet{
		ID:        fmt.Sprintf("raw-%d", time.Now().UnixNano()),
		Protocol:  runtime.ProtocolRaw,
		Source:    clientAddr,
		Dest:      clientConn.LocalAddr().String(), // Destino padrão (pode ser reescrito pelo worker)
		IsInbound: true,
		Data:      payload,
	}

	engine := p.getWorkerEngine()
	var result *runtime.PacketResult
	if engine != nil {
		var errProcess error
		result, errProcess = engine.ProcessPacket(packet)
		if errProcess != nil {
			if p.metrics != nil {
				telemetry.RecordError(p.metrics, ctx, "raw_tcp_worker_failed", "raw")
			}
			// Failsafe: se der erro, assume permitir ou não de acordo com failsafe do config
			if p.Config().Runtime.Failsafe == "block" {
				return
			}
			result = &runtime.PacketResult{Allow: true, Packet: packet}
		}
	} else {
		result = &runtime.PacketResult{Allow: true, Packet: packet}
	}

	if !result.Allow {
		return
	}

	destAddr := result.Packet.Dest
	// Previne loops de conexão apontando para o próprio proxy
	if destAddr == "" || destAddr == clientConn.LocalAddr().String() {
		if envDest := os.Getenv("SONIC_TEST_RAW_UPSTREAM"); envDest != "" {
			destAddr = envDest
		} else {
			// Sem destino de upstream válido definido pelo worker ou ambiente
			return
		}
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	dialer := proxy.FromEnvironment()
	var serverConn net.Conn
	if cDialer, ok := dialer.(interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}); ok {
		serverConn, err = cDialer.DialContext(dialCtx, "tcp", destAddr)
	} else {
		serverConn, err = dialer.Dial("tcp", destAddr)
	}
	if err != nil {
		if p.metrics != nil {
			telemetry.RecordError(p.metrics, ctx, "raw_tcp_upstream_connect_failed", destAddr)
		}
		return
	}
	defer serverConn.Close()

	// Escreve os bytes brutos (opcionalmente modificados pelo worker) no servidor de destino
	if len(result.Packet.Data) > 0 {
		n, err := serverConn.Write(result.Packet.Data)
		if err != nil {
			return
		}
		if n > 0 {
			if p.metrics != nil {
				telemetry.RecordBytesTransferred(p.metrics, ctx, int64(n))
			}
		}
	}

	// Wrap connections with a 5-minute idle timeout to prevent leaks
	idleTimeout := 5 * time.Minute
	tClientConn := newTimeoutConn(clientConn, idleTimeout)
	tServerConn := newTimeoutConn(serverConn, idleTimeout)

	// Encaminhamento transparente bidirecional com varredura contínua contra exploits
	var wg sync.WaitGroup
	wg.Add(2)

	var clientReader io.Reader = tClientConn
	if p.cveScanner != nil {
		clientReader = security.NewScanningReader(tClientConn, p.cveScanner)
	}

	go func() {
		defer wg.Done()
		_, _ = io.Copy(tServerConn, clientReader)
		_ = tServerConn.Close()
		_ = tClientConn.Close()
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(tClientConn, tServerConn)
		_ = tClientConn.Close()
		_ = tServerConn.Close()
	}()

	wg.Wait()

	if p.metrics != nil {
		telemetry.RecordRequest(p.metrics, ctx, destAddr, "raw_tcp", 0, time.Since(start))
	}
}

// timeoutConn resets read/write deadlines on every Read/Write to prevent goroutine leaks on idle/hung connections.
type timeoutConn struct {
	net.Conn
	timeout time.Duration
}

func newTimeoutConn(c net.Conn, timeout time.Duration) net.Conn {
	return &timeoutConn{Conn: c, timeout: timeout}
}

func (tc *timeoutConn) Read(b []byte) (n int, err error) {
	if tc.timeout > 0 {
		_ = tc.Conn.SetReadDeadline(time.Now().Add(tc.timeout))
	}
	return tc.Conn.Read(b)
}

func (tc *timeoutConn) Write(b []byte) (n int, err error) {
	if tc.timeout > 0 {
		_ = tc.Conn.SetWriteDeadline(time.Now().Add(tc.timeout))
	}
	return tc.Conn.Write(b)
}
