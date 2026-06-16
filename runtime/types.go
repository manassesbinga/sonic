// Package runtime provides a multi-language, multi-protocol execution engine.
//
// It supports:
//   - Multiple protocols (HTTP, TCP, UDP, DNS, WebSocket, gRPC, QUIC) via a protocol-agnostic Packet structure
//   - Multiple languages/runtimes (JavaScript via Goja, WebAssembly, native executables via stdin/stdout)
//   - Shared state via a thread-safe KV Store
//
// Usage:
//
//	kv := runtime.NewKVStore()
//	manager := runtime.NewWorkerManager(kv)
//	packet := &runtime.Packet{Protocol: runtime.ProtocolHTTP, ...}
//	result, err := manager.ProcessPacket(packet)
package runtime

// Protocol represents the type of network protocol.
type Protocol string

const (
	ProtocolHTTP      Protocol = "http"
	ProtocolHTTPS     Protocol = "https"
	ProtocolTCP       Protocol = "tcp"
	ProtocolUDP       Protocol = "udp"
	ProtocolDNS       Protocol = "dns"
	ProtocolWebSocket Protocol = "websocket"
	ProtocolGRPC      Protocol = "grpc"
	ProtocolQUIC      Protocol = "quic"
	ProtocolRaw       Protocol = "raw"
)

// Packet is a protocol-agnostic structure representing ANY network data.
// Whether it's an HTTP request, DNS query, or TCP stream - it arrives as a Packet.
type Packet struct {
	// Core metadata
	ID       string   `json:"id"`
	Protocol Protocol `json:"protocol"`
	Source   string   `json:"source"`   // Source address (IP:port)
	Dest     string   `json:"dest"`     // Destination address (IP:port)
	IsInbound bool    `json:"isInbound"` // true = incoming to proxy, false = outgoing from proxy

	// Protocol-specific data (HTTP, DNS, etc.)
	Meta map[string]interface{} `json:"meta"`

	// Raw bytes
	Data []byte `json:"data"`

	// For compatibility with existing HTTP workers
	Request  *Request  `json:"request,omitempty"`
	Response *Response `json:"response,omitempty"`
}

// PacketResult holds the result of processing a Packet.
type PacketResult struct {
	// Whether to allow the packet to continue (false = block/drop)
	Allow bool `json:"allow"`

	// Modified packet (if changed)
	Packet *Packet `json:"packet,omitempty"`

	// Direct response (for protocols that support it, like HTTP)
	Response *Response `json:"response,omitempty"`
}

// Request represents an intercepted HTTP request (kept for backward compatibility).
type Request struct {
	ID         string            `json:"id"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	ClientAddr string            `json:"clientAddr"` // Client address (IP:port)
}

// Response represents an intercepted HTTP response (kept for backward compatibility).
type Response struct {
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// TrafficResult holds the result of onTraffic execution (kept for backward compatibility).
type TrafficResult struct {
	IsResponse bool              `json:"_isResponse"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Path       string            `json:"path"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}
