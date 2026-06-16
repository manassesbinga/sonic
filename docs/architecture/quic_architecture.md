# QUIC & HTTP/3 Interception Architecture

This document describes the planned implementation architecture for QUIC and HTTP/3 Man-in-the-Middle (MITM) decryption, packet interception, and worker execution inside the Sonic Edge Engine.

## 1. Overview
QUIC is a UDP-based transport protocol that integrates TLS 1.3 directly into its handshake, serving as the foundation for HTTP/3. Intercepting QUIC/HTTP/3 requires decrypting QUIC connection streams, negotiating TLS 1.3 handshakes dynamically with spoofed certificates, exposing HTTP/3 requests to Workers, and re-encrypting them to upstream servers.

```
       Client ──────► UDP Port 8443 (QUIC Interceptor)
                            │
                            ├─ 1. Peek UDP packet (Initial connection)
                            ├─ 2. Terminate QUIC TLS 1.3 Handshake (Dynamic CA)
                            │
                            └─ HTTP/3 Stream Loop:
                                 ├─ Read HTTP/3 Request Frames (Stream ID)
                                 ├─ Execute Edge Workers (onTraffic)
                                 ├─ Connect Upstream QUIC Session (with strict TLS)
                                 ├─ Forward HTTP/3 Request
                                 ├─ Read HTTP/3 Response Frames
                                 ├─ Execute Edge Workers (onResponse)
                                 └─ Send HTTP/3 Response to Client
```

---

## 2. Library Integration & TLS 1.3 Termination
Sonic will integrate the robust `github.com/quic-go/quic-go` package to handle the low-level QUIC transport framing and HTTP/3 frame decoding.

### 2.1 QUIC TLS MITM Handshake
In TCP TLS, we peek at the SNI in the ClientHello. In QUIC:
1. The client sends a QUIC `Initial` packet containing the TLS ClientHello cryptographically protected.
2. Sonic intercepts the packet on UDP port 8443.
3. The `quic.Listener` is configured with a dynamic `GetConfigForClient` callback in `tls.Config`.
4. We extract the Server Name Indication (SNI) from the ClientHello, generate a dynamic leaf certificate matching the SNI using the internal Root CA, and complete the QUIC TLS 1.3 handshake.

```go
// Proposed implementation structure for the QUIC MITM Listener
listener, err := quic.ListenAddr(":8443", &tls.Config{
    GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
        // Generate certificates on-the-fly from CA matching info.ServerName
        cert, err := certCache.GetCertificate(info.ServerName)
        if err != nil {
            return nil, err
        }
        return &tls.Config{
            Certificates: []tls.Certificate{*cert},
            NextProtos:   []string{"h3", "h3-29"}, // ALPN for HTTP/3
        }, nil
    },
}, nil)
```

---

## 3. HTTP/3 Stream Multiplexing & Worker Execution
QUIC multiplexes several virtual streams over a single UDP connection. Each stream represents an HTTP/3 request/response lifecycle.

### 3.1 Stream Processing
1. When a client opens a bidirectional stream (e.g. Stream ID 4), Sonic's HTTP/3 server decodes the HTTP/3 request headers and body frames.
2. The request is mapped to a standard `Packet` (`packet.Protocol = "https"` or `"http"` with ALPN metadata).
3. The packet is processed by the `WorkerManager`.
4. Sonic opens an outbound QUIC session to the real upstream server, establishes a stream, writes the modified headers/body, reads the response, runs it through `onResponse` workers, and writes it back to the client QUIC stream.

---

## 4. Performance & OS Optimizations
Because UDP and QUIC run entirely in user-space, they generate high context-switch overhead compared to TCP kernel splicing. To maintain Sonic's performance standards:
- **UDP GRO/GSO**: Enable Generic Receive Offload (GRO) and Generic Segment Offload (GSO) on the Linux sockets to bundle multiple UDP packets into a single read/write syscall.
- **eBPF Redirection**: While eBPF `SK_MSG` is TCP-focused, we will implement `xdp` (eXpress Data Path) kernel hooks to parse UDP port 443 packets and perform raw steering, accelerating the initial handshake packets into the user-space QUIC listener.
