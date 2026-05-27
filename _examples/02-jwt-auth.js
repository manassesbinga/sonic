/**
 * EXEMPLO 2: Autenticação JWT + Cache no KV
 * 
 * Valida tokens JWT e cacheia resultados no KV Store para performance.
 */

function onTraffic(request) {
  const authHeader = request.headers.get("Authorization");
  
  // Rotas públicas (não precisam de autenticação)
  const publicPaths = ["/login", "/signup", "/health"];
  const path = request.path || new URL(request.url).pathname;
  
  if (publicPaths.includes(path)) {
    return request;
  }

  if (!authHeader || !authHeader.startsWith("Bearer ")) {
    return new Response("Unauthorized", {
      status: 401,
      headers: { "WWW-Authenticate": "Bearer" },
    });
  }

  const token = authHeader.slice(7);
  const cacheKey = `jwt:${token}`;

  // Verificar cache no KV Store
  let cached = kv.get(cacheKey);
  if (cached) {
    const user = JSON.parse(cached);
    request.headers.set("X-User-ID", user.id);
    request.headers.set("X-User-Role", user.role);
    return request;
  }

  // Validação real (simulada - use jwtVerify em produção)
  const isValid = jwtVerify(token, "seu-segredo-aqui");
  
  if (!isValid) {
    return new Response("Invalid token", { status: 403 });
  }

  // Simular extração de dados do token
  const user = {
    id: "user_123",
    role: "admin",
    exp: Date.now() + 3600000, // 1 hora
  };

  // Cachear no KV Store por 1 hora
  kv.set(cacheKey, JSON.stringify(user));

  // Adicionar headers de usuário para o upstream
  request.headers.set("X-User-ID", user.id);
  request.headers.set("X-User-Role", user.role);

  return request;
}
