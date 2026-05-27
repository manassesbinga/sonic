/**
 * EXEMPLO 5: Proxy para Next.js API Routes
 * 
 * Use o Sonic como proxy para suas APIs Next.js, adicionando:
 * - Rate limiting
 * - Autenticação centralizada
 * - Cache de respostas
 * - Headers de segurança
 */

function onTraffic(request) {
  const url = new URL(request.url);
  
  // 1. Rate Limiting para API routes
  if (url.pathname.startsWith("/api/")) {
    const ip = request.headers.get("X-Real-IP") || "unknown";
    const key = `next:api:rate:${ip}`;
    let count = parseInt(kv.get(key) || "0");
    
    if (count >= 50) { // 50 requests/minuto
      return new Response(JSON.stringify({
        error: "Too Many Requests",
        message: "Try again later"
      }), {
        status: 429,
        headers: { "Content-Type": "application/json" }
      });
    }
    
    count++;
    kv.set(key, count.toString());
    
    // Resetar após 1 minuto (simples - em produção use timestamp)
    setTimeout(() => kv.delete(key), 60000);
  }

  // 2. Adicionar headers CORS para desenvolvimento
  request.headers.set("Access-Control-Allow-Origin", "*");
  request.headers.set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS");

  // 3. Adicionar headers de segurança
  request.headers.set("X-Frame-Options", "DENY");
  request.headers.set("X-Content-Type-Options", "nosniff");

  return request;
}

function onResponse(response) {
  // 1. Cachear respostas de API GET por 10 segundos
  if (response.status === 200) {
    response.headers.set("Cache-Control", "public, max-age=10");
  }

  // 2. Remover headers sensíveis do Next.js
  response.headers.delete("X-Powered-By");

  // 3. Adicionar header customizado
  response.headers.set("X-Sonic-Proxy", "active");

  return response;
}
