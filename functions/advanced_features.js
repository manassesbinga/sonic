// functions/advanced_features.js — Sonic Edge Worker
//
// Demonstrates advanced features of Sonic:
// - Stateful rate limiting and counters via KV Store (shared state)
// - Outbound requests via Web Standard fetch()
// - Neural Cache monitoring and cleanup
// - Cryptographically secure JWT Verification

function onTraffic(request) {
    log("Advanced JS worker: " + request.method + " " + request.url);

    // 1. JWT verification test
    const authHeader = request.headers.get("Authorization");
    if (authHeader && authHeader.startsWith("Bearer ")) {
        const token = authHeader.substring(7);
        // Verify token with secret key
        const secret = "my_jwt_shared_secret_key";
        const isValid = jwtVerify(token, secret);
        
        if (isValid) {
            log("JWT Verification Succeeded for request.");
            request.headers.set("X-User-Authenticated", "true");
        } else {
            log("JWT Verification Failed.");
            // Direct WAF block by returning a Response
            return new Response("Unauthorized: Invalid JWT signature", {
                status: 401,
                headers: { "Content-Type": "text/plain" }
            });
        }
    }

    // 2. State management with KV store
    if (typeof kv !== "undefined" && kv !== null) {
        let count = parseInt(kv.get("requests_count") || "0");
        count++;
        kv.set("requests_count", count.toString());
        log("Global Request Count in KV Store: " + count);
        request.headers.set("X-Total-KV-Requests", count.toString());

        // Simple client rate limiter (using Client IP or fallback)
        const clientIP = request.clientAddr || "unknown-ip";
        const key = "rate:" + clientIP;
        let reqs = parseInt(kv.get(key) || "0");
        if (reqs > 100) {
            log("Client " + clientIP + " rate limited.");
            return new Response("Too Many Requests", {
                status: 429,
                headers: { "Content-Type": "text/plain" }
            });
        }
        kv.set(key, (reqs + 1).toString(), 60000); // 1-minute TTL
    }

    // 3. Neural Cache inspection
    if (typeof neuralCache !== "undefined" && neuralCache !== null) {
        const stats = neuralCache.stats();
        log("Neural Cache Status: Hits=" + stats.hits + ", Misses=" + stats.misses + ", Size=" + stats.size);
    }

    return request;
}

function onResponse(response) {
    // Add custom header to notify the client
    response.headers.set("X-Sonic-Advanced-Worker", "enabled");
    return response;
}
