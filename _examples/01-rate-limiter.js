/**
 * EXEMPLO 1: Rate Limiting com KV Store
 * 
 * Limita cada IP a 100 requisições por minuto.
 * Usa o KV Store persistente para manter o estado.
 */

function onTraffic(request) {
  const ip = request.headers.get("X-Real-IP") || "unknown";
  const key = `rate:${ip}`;
  const now = Date.now();
  const windowMs = 60000; // 1 minuto
  const limit = 100;

  let state = kv.get(key);
  if (state) {
    state = JSON.parse(state);
  } else {
    state = { count: 0, resetAt: now + windowMs };
  }

  // Resetar se a janela expirou
  if (now > state.resetAt) {
    state = { count: 0, resetAt: now + windowMs };
  }

  state.count++;

  // Salvar no KV Store
  kv.set(key, JSON.stringify(state));

  // Adicionar headers informativos
  request.headers.set("X-RateLimit-Limit", limit.toString());
  request.headers.set("X-RateLimit-Remaining", Math.max(0, limit - state.count).toString());
  request.headers.set("X-RateLimit-Reset", Math.ceil(state.resetAt / 1000).toString());

  // Bloquear se excedeu o limite
  if (state.count > limit) {
    return new Response("Too Many Requests", {
      status: 429,
      headers: {
        "Content-Type": "text/plain",
        "Retry-After": Math.ceil((state.resetAt - now) / 1000).toString(),
      },
    });
  }

  return request;
}
