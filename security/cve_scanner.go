package security

import (
	"errors"
	"io"
	"regexp"
	"sync"
	"time"
)

// SecurityIncident representa um alerta de vulnerabilidade detectado.
type SecurityIncident struct {
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
	Preview   string    `json:"preview"`
}

var (
	incidentLog      []SecurityIncident
	incidentLogLimit = 100
	incidentLogMu    sync.RWMutex
)

// AddSecurityIncident adiciona um novo alerta de segurança ao histórico em memória.
func AddSecurityIncident(signature, preview string) {
	incidentLogMu.Lock()
	defer incidentLogMu.Unlock()

	// Truncar preview para evitar uso excessivo de memória (limite de 512 caracteres)
	previewText := preview
	if len(previewText) > 512 {
		previewText = previewText[:512] + "..."
	}

	incident := SecurityIncident{
		Timestamp: time.Now(),
		Signature: signature,
		Preview:   previewText,
	}

	incidentLog = append(incidentLog, incident)
	if len(incidentLog) > incidentLogLimit {
		incidentLog = incidentLog[len(incidentLog)-incidentLogLimit:]
	}
}

// GetSecurityIncidents retorna a lista dos últimos alertas de segurança.
func GetSecurityIncidents() []SecurityIncident {
	incidentLogMu.RLock()
	defer incidentLogMu.RUnlock()

	copyLog := make([]SecurityIncident, len(incidentLog))
	copy(copyLog, incidentLog)
	return copyLog
}

// ErrCVEDetected is returned when a security rule match is found in the stream.
var ErrCVEDetected = errors.New("security rule violation: CVE/vulnerability exploit payload detected")

// SecurityEvent represents events emitted by the scanner
type SecurityEvent string

const (
	EventScanStarted   SecurityEvent = "scan_started"
	EventCVEDetected   SecurityEvent = "cve_detected"
	EventScanCompleted SecurityEvent = "scan_completed"
)

// SecurityEventHandler is a callback function for security events
type SecurityEventHandler func(event SecurityEvent, details map[string]interface{})

var defaultSignatures = []string{
	// JavaScript
	`(?i)(__proto__|constructor\s*(\[\s*['"]prototype['"]\s*\]|\.\s*prototype))`,
	`(?i)(eval\s*\(\s*.*req|new\s+Function\s*\(\s*.*req)`,
	`(?i)(pac-resolver.*exec|FindProxyForURL.*eval)`,
	// Python
	`(?i)(cos\n(system|subprocess)|cposix\n(system|subprocess)|pickle\.loads)`,
	`(?i)(eval\s*\(\s*input|exec\s*\(\s*|__import__\s*\(\s*['"]os['"]\))`,
	// Bash / Shell
	`\(\)\s*\{\s*[^}]*\}\s*;`,
	`(?i)(;\s*(rm|cat|curl|wget|sh|bash|python|nc|exec)\s+)`,
	// General / Path Traversal
	`\.\./\.\./\.\./`,
}

// CVEStreamScanner implements streaming CVE signature detection
type CVEStreamScanner struct {
	signatures []*regexp.Regexp
	onEvent    SecurityEventHandler
	windowSize int
	Block      bool // If true, matches will halt the stream and return an error. If false, it only triggers events.
}

// NewCVEStreamScanner creates a new CVEStreamScanner with default signatures
func NewCVEStreamScanner(onEvent SecurityEventHandler) *CVEStreamScanner {
	sigs := make([]*regexp.Regexp, 0, len(defaultSignatures))
	for _, sigStr := range defaultSignatures {
		if r, err := regexp.Compile(sigStr); err == nil {
			sigs = append(sigs, r)
		}
	}
	return &CVEStreamScanner{
		signatures: sigs,
		onEvent:    onEvent,
		windowSize: 4096, // 4KB sliding window
		Block:      true,
	}
}

// Scan scans a byte slice against the compiled signatures and returns true plus the matching pattern if detected.
func (s *CVEStreamScanner) Scan(data []byte) (bool, string) {
	for _, sig := range s.signatures {
		if sig.Match(data) {
			return true, sig.String()
		}
	}
	return false, ""
}

// ScanningReader wraps an io.Reader and performs sliding-window signature scanning on the fly.
type ScanningReader struct {
	r          io.Reader
	scanner    *CVEStreamScanner
	buffer     []byte
	err        error
	scanBegun  bool
}

// NewScanningReader wraps an io.Reader with security scanning.
func NewScanningReader(r io.Reader, scanner *CVEStreamScanner) *ScanningReader {
	if r == nil {
		return nil
	}
	if scanner == nil {
		return &ScanningReader{r: r}
	}
	return &ScanningReader{
		r:       r,
		scanner: scanner,
		buffer:  make([]byte, 0, scanner.windowSize*2),
	}
}

// Read reads from the underlying reader, feeds the sliding window, scans it, and returns ErrCVEDetected if a match is found.
func (sr *ScanningReader) Read(p []byte) (n int, err error) {
	if sr.err != nil {
		return 0, sr.err
	}
	if sr.scanner == nil {
		return sr.r.Read(p)
	}

	n, err = sr.r.Read(p)
	if n > 0 {
		if !sr.scanBegun {
			sr.scanBegun = true
			if sr.scanner.onEvent != nil {
				sr.scanner.onEvent(EventScanStarted, nil)
			}
		}

		// Append new bytes to sliding window buffer
		sr.buffer = append(sr.buffer, p[:n]...)

		// Scan buffer containing all newly read bytes
		if matched, sig := sr.scanner.Scan(sr.buffer); matched {
			if sr.scanner.onEvent != nil {
				sr.scanner.onEvent(EventCVEDetected, map[string]interface{}{
					"signature": sig,
					"preview":   string(sr.buffer),
				})
			}
			if sr.scanner.Block {
				sr.err = ErrCVEDetected
				return 0, ErrCVEDetected
			}
		}

		// Slide window if buffer size exceeds windowSize to retain history for next read
		if len(sr.buffer) > sr.scanner.windowSize {
			sr.buffer = sr.buffer[len(sr.buffer)-sr.scanner.windowSize:]
		}
	}

	if err == io.EOF {
		if sr.scanner.onEvent != nil {
			sr.scanner.onEvent(EventScanCompleted, nil)
		}
	} else if err != nil {
		sr.err = err
	}

	return n, err
}

// Close closes the underlying reader if it implements io.Closer.
func (sr *ScanningReader) Close() error {
	if closer, ok := sr.r.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
