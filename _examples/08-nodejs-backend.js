/**
 * EXEMPLO 8: Proxy para Node.js Backend (Express, NestJS, Fastify)
 * 
 * Adiciona features sem modificar o código do seu backend!
 */

function onTraffic(request) {
  const url = new URL(request.url);

  // 1. Rate Limiting global para o backend
  const ip = request.headers.get("X-Real-IP") || "unknown";
  const key = `node:rate:${ip}`;
  let count = parseInt(kv.get(key) || "0");
  
  if (count >= 200) {
    return new Response(JSON.stringify({
      error: "Too Many Requests",
      code: "RATE_LIMIT_EXCEEDED"
    }), {
      status: 429,
      headers: { "Content-Type": "application/json" }
    });
  }
  count++;
  kv.set(key, count.toString());

  // 2. Sanitizar inputs para evitar XSS/SQL Injection
  if (request.body) {
    let body = request.body;
    // Remove tags HTML simples
    body = body.replace(/<[^>]*>/g, "");
    request.body = body;
  }

  // 3. Adicionar ID da requisição para tracing
  const reqId = "req_" + Date.now() + "_" + Math.random().toString(36).substr(2, 6);
  request.headers.set("X-Request-ID", reqId);
  kv.set(`req:${reqId}`, JSON.stringify({
    method: request.method,
    url: request.url,
    startTime: Date.now()
  }));

  return request;
}

function onResponse(response) {
  const reqId = response.headers.get("X-Request-ID");
  
  if (reqId) {
    const reqData = kv.get(`req:${reqId}`);
    if (reqData) {
      const req = JSON.parse(reqData);
      const latency = Date.now() - req.startTime;
      
      // Log de performance
      log(JSON.stringify({
        request_id: reqId,
        method: req.method,
        url: req.url,
        status: response.status,
        latency_ms: latency
      }));
      
      // Limpar
      kv.delete(`req:${reqId}`);
    }
  }

  return response;
}
