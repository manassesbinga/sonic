/**
 * EXEMPLO 3: Transformação de Requests/Responses
 * 
 * Modifica requests e responses em tempo real.
 */

function onTraffic(request) {
  // 1. Adicionar header de tracing
  const traceId = kv.get("trace:next") || "trace_0";
  kv.set("trace:next", (parseInt(traceId.split("_")[1] || 0) + 1).toString());
  request.headers.set("X-Trace-ID", traceId);

  // 2. Reescrita de URL (ex: /api/v1/ -> /v2/)
  if (request.url.includes("/api/v1/")) {
    request.url = request.url.replace("/api/v1/", "/api/v2/");
    request.headers.set("X-API-Version", "2");
  }

  // 3. Adicionar metadados do cliente
  request.headers.set("X-Sonic-Version", "1.1.0");
  request.headers.set("X-Request-Time", Date.now().toString());

  return request;
}

function onResponse(response) {
  // 1. Adicionar headers de segurança
  response.headers.set("X-Content-Type-Options", "nosniff");
  response.headers.set("X-Frame-Options", "DENY");
  response.headers.set("X-XSS-Protection", "1; mode=block");

  // 2. Adicionar informações do Sonic
  response.headers.set("X-Processed-By", "Sonic");

  // 3. Medir latência
  const startTime = parseInt(response.headers.get("X-Request-Time") || "0");
  if (startTime > 0) {
    const latency = Date.now() - startTime;
    response.headers.set("X-Sonic-Latency", latency + "ms");
    
    // Registrar latência no KV para métricas
    const latencyKey = "metrics:latency:" + new Date().toISOString().slice(0, 13); // por hora
    let total = parseInt(kv.get(latencyKey) || "0");
    kv.set(latencyKey, (total + latency).toString());
  }

  return response;
}
