# UDP Transparent Proxy Architecture

This document describes the planned implementation architecture for UDP transparent proxying and worker logic execution inside the Sonic Edge Engine.

## 1. Overview
Unlike TCP, UDP is a connectionless protocol. Transparently intercepting and executing logic over UDP requires creating a virtual "session tracking" layer to identify packet streams and route them dynamically through Sonic Edge Workers.

```
                  ┌───────────────────────────────┐
                  │      eBPF UDP Intercept       │
                  └───────────────┬───────────────┘
                                  │ (Redirect to port 8443 UDP)
                                  ▼
                  ┌───────────────────────────────┐
                  │    Sonic UDP Proxy Listener   │
                  └───────────────┬───────────────┘
                                  │
                                  ▼
                  ┌───────────────────────────────┐
                  │     Virtual Session Table     │
                  │   [Src IP/Port ↔ Dst IP/Port] │
                  └───────────────┬───────────────┘
                                  │
                                  ▼
                  ┌───────────────────────────────┐
                  │        Worker Execution       │
                  │   (Process UDP Packet Data)   │
                  └───────────────┬───────────────┘
                                  │
                                  ▼
                  ┌───────────────────────────────┐
                  │   Upstream UDP Forwarder      │
                  └───────────────────────────────┘
```

---

## 2. Core Components

### 2.1 Virtual Session Tracker
To support request-response semantics in workers, we will introduce a thread-safe `UDPSessionTable`.
- **Key**: A hash of source IP + port and destination IP + port: `hash(src_ip:src_port, dst_ip:dst_port)`.
- **Value**: A struct holding:
  - `ClientAddr *net.UDPAddr`
  - `UpstreamConn *net.UDPConn`
  - `LastActivity time.Time`
- **Reaper**: A background goroutine that runs every 10 seconds to purge idle sessions (e.g., sessions with no traffic for > 30 seconds) and close their respective upstream sockets.

### 2.2 UDP Listener and Multiplexer
The listener binds to the configured UDP intercept port using `net.ListenUDP`.
- **Reading**: Reads packets into a pre-allocated byte buffer pool (`sync.Pool`).
- **Dispatching**: For each incoming packet:
  1. Lookup the virtual session in the `UDPSessionTable`.
  2. If not exists, create a new connection to the destination upstream server (`net.DialUDP`) and register the session.
  3. Start a bidirectional forwarder loop for that session.

---

## 3. Worker Integration and Payload Serialization

The raw UDP payload is wrapped into a standard `Packet` structure:
- `packet.Protocol` = `"udp"`
- `packet.Data` = Raw bytes of the UDP datagram.
- `packet.Source` = `src_ip:src_port`
- `packet.Dest` = `dst_ip:dst_port`

### 3.1 JavaScript VM Bindings
Workers can inspect and rewrite UDP packets. Because UDP does not have HTTP headers, JS workers use a byte array structure:
```javascript
function onTraffic(packet) {
    log("UDP packet received from: " + packet.source);
    
    // Inspect payload
    let data = packet.data;
    if (data[0] === 0x01) {
        // Modify bytes
        data[0] = 0x02;
        packet.data = data;
    }
    
    // Reroute destination dynamically
    packet.dest = "10.0.0.5:9000";
    return packet;
}
```

---

## 4. eBPF & Kernel Splicing for UDP
For high-performance UDP routing under Linux:
- Attach a BPF program of type `BPF_PROG_TYPE_SK_LOOKUP` or use iptables `TPROXY` on UDP ports to redirect traffic to Sonic's UDP port without altering the packet headers.
- Utilize `BPF_MAP_TYPE_SOCKHASH` to route datagrams directly in kernel space between client socket and upstream socket once the worker allows the stream, bypassing user-space for subsequent packets in the same stream.
