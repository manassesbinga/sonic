/**
 * EXEMPLO 6: Autenticação Centralizada para Next.js
 * 
 * Protege suas páginas e APIs Next.js sem modificar o código do app!
 */

function onTraffic(request) {
  const url = new URL(request.url);
  const path = url.pathname;

  // Rotas públicas (não precisam de login)
  const publicPaths = [
    "/",
    "/login",
    "/signup",
    "/_next/",
    "/static/",
    "/favicon.ico",
    "/api/auth/login",
    "/api/auth/signup"
  ];

  // Verificar se é rota pública
  const isPublic = publicPaths.some(p => path.startsWith(p));
  if (isPublic) {
    return request;
  }

  // Verificar token de sessão
  const sessionCookie = request.headers.get("cookie") || "";
  const sessionMatch = sessionCookie.match(/session_token=([^;]+)/);
  const sessionToken = sessionMatch ? sessionMatch[1] : null;

  if (!sessionToken) {
    // Redirecionar para login
    return new Response(null, {
      status: 302,
      headers: {
        "Location": "/login?redirect=" + encodeURIComponent(path)
      }
    });
  }

  // Validar sessão no KV Store
  const sessionKey = `session:${sessionToken}`;
  const sessionData = kv.get(sessionKey);
  
  if (!sessionData) {
    return new Response(null, {
      status: 302,
      headers: { "Location": "/login" }
    });
  }

  const session = JSON.parse(sessionData);
  
  // Adicionar dados do usuário aos headers para o Next.js
  request.headers.set("X-User-ID", session.userId);
  request.headers.set("X-User-Email", session.email);

  return request;
}
