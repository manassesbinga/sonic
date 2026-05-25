// functions/hello.js — Sonic Edge Worker
//
// Compatible with Cloudflare Workers API (Request, Response, Headers, fetch)
// Extended with Node.js-style require() for local modules.
//
// API:
//   Sonic                            Cloudflare Workers
//   ─────                            ──────────────────
//   onTraffic(request)  ──>          fetch(event)
//   onResponse(response) ──>         (after fetch)
//   require("module")                import/require
//   log(msg)                         console.log
//   jwtVerify(token, secret)         Web Crypto API

function onTraffic(request) {
    log("Intercepted: " + request.method + " " + request.url);

    // Add tracking headers (like Cloudflare's CF-Ray)
    request.headers.set("X-Sonic-Worker", "active");
    request.headers.set("X-Request-ID", generateId());

    // WAF: block requests with suspicious headers
    if (request.headers.get("x-block-me") === "true") {
        return new Response("Blocked by Sonic Edge WAF", {
            status: 403,
            headers: { "X-Sonic-WAF": "active" }
        });
    }

    return request;
}

function onResponse(response) {
    response.headers.set("X-Sonic-Proxy", "enabled");
    return response;
}

function generateId() {
    return Math.random().toString(36).substring(2, 10) +
           "-" + Date.now().toString(36);
}
