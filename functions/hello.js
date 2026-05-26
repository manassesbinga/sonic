// functions/hello.js — Sonic Edge Worker
//
// Compatible with Cloudflare Workers API:
//   - Request, Response, Headers, fetch
//
// onTraffic:  intercept/modify requests before they reach the server
// onResponse: intercept/modify responses before they reach the client

function onTraffic(request) {
    log("Intercepted: " + request.method + " " + request.url);
    request.headers.set("X-Sonic-Worker", "active");
    request.headers.set("X-Request-Time", Date.now().toString());
    return request;
}

function onResponse(response) {
    response.headers.set("X-Sonic-Proxy", "enabled");
    return response;
}
