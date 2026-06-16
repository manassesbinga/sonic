# DNS Interception & Resolving Architecture

This document describes the planned implementation architecture for DNS transparent proxying, packet parsing, and dynamic DNS worker scripts inside the Sonic Edge Engine.

## 1. Overview
DNS queries primarily run over UDP (and optionally TCP) on Port 53. Sonic will intercept these queries, parse the raw binary DNS frames, expose them to Edge Workers, and allow scripts to dynamically resolve, block, or rewrite DNS records before returning the response to the client.

```
                   Client Request (DNS Query on Port 53)
                                 │
                                 ▼
                     eBPF / iptables Redirection
                                 │
                                 ▼
                    Sonic DNS Server (Port 8053)
                                 │
                       [dnsmessage Decoder]
                                 │
                                 ▼
                      Edge Worker Executed
              (e.g., query: "api.internal", type: "A")
               ┌─────────────────┴─────────────────┐
               ▼ (Allow / Resolve)                 ▼ (Block / Rewrite)
        Outbound Resolving                     Forge Answer
   (Upstream: 1.1.1.1 / 8.8.8.8)          (Return "127.0.0.1" / NXDOMAIN)
               └─────────────────┬─────────────────┘
                                 │
                       [dnsmessage Encoder]
                                 │
                                 ▼
                           Client Response
```

---

## 2. Packet Parsing & Serialization
To parse DNS messages without third-party dependencies, Sonic will utilize Go's standard library `golang.org/x/net/dns/dnsmessage`.

### 2.1 Parsing Sequence
1. The DNS proxy reads the binary payload from UDP/TCP socket.
2. Initialize `dnsmessage.Parser` to extract the Header, Questions, and existing Answers.
3. Construct a standard `Packet` with:
   - `packet.Protocol` = `"dns"`
   - `packet.Source` = Client IP + Port
   - `packet.DNS` = Struct mapping fields:
     - `ID`: Message Transaction ID
     - `OpCode`: Query OpCode (Standard, Inverse, Status)
     - `Questions`: Array of `{ Name: string, Type: string, Class: string }`

---

## 3. Worker API Extensions
JavaScript workers will be able to intercept DNS packets using standard handlers. We will introduce DNS-specific helper objects in the bootstrap environment:

### 3.1 JavaScript DNS Worker Example
```javascript
function onTraffic(request) {
    if (request.protocol !== "dns") return request;
    
    const dnsQuery = request.dns;
    log("DNS Query for domain: " + dnsQuery.questions[0].name);
    
    // 1. Dynamic Ad Blocking / Security Shield
    if (dnsQuery.questions[0].name.includes("malware-domain.com")) {
        return dns.block(dnsQuery, "NXDOMAIN"); // Returns Name Error
    }
    
    // 2. Custom Internal Domain Resolution
    if (dnsQuery.questions[0].name === "portal.sonic.local") {
        return dns.forge(dnsQuery, {
            type: "A",
            ttl: 300,
            address: "192.168.1.100"
        });
    }
    
    // 3. Dynamic Rerouting to private resolvers
    if (dnsQuery.questions[0].name.endsWith(".corp.internal")) {
        request.dns.upstream = "10.0.0.1:53"; // Forward to corporate DNS
    }
    
    return request;
}
```

### 3.2 Native Bindings
We will implement the following Go bindings in `runtime/bootstrap.go` to support DNS actions:
- `dns.block(query, reason)`: Builds a response with `RCodeNameError` (NXDOMAIN) or `RCodeRefused`.
- `dns.forge(query, records...)`: Encodes custom resource records (A, AAAA, CNAME, TXT, MX) into the Answer section of the response.

---

## 4. Local DNS Caching
To maintain sub-millisecond response times:
- Sonic will implement an in-memory, concurrent DNS cache (`sync.Map`) keyed by `hash(domain:query_type)`.
- Respect TTLs returned by upstream resolvers or defined by workers.
- The cache will dynamically clear expired entries and support administrative purge requests via the WebUI.
