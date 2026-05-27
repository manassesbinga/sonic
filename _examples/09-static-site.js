/**
 * EXEMPLO 9: Proxy para Site Estático (HTML/CSS/JS, Hugo, Jekyll, Gatsby)
 * 
 * Melhora a performance e segurança de sites estáticos!
 */

function onTraffic(request) {
  const url = new URL(request.url);
  const path = url.pathname;

  // 1. Cachear tudo por muito tempo (exceto HTML)
  if (!path.endsWith(".html") && path.includes(".")) {
    request.headers.set("Cache-Control", "public, max-age=31536000, immutable");
  } else if (path.endsWith(".html")) {
    // HTML: cache curto, para atualizações rápidas
    request.headers.set("Cache-Control", "public, max-age=300");
  }

  // 2. Redirecionar www para non-www (ou vice-versa)
  if (url.hostname.startsWith("www.")) {
    const newUrl = url.protocol + "//" + url.hostname.slice(4) + url.pathname + url.search;
    return new Response(null, {
      status: 301,
      headers: { "Location": newUrl }
    });
  }

  // 3. Adicionar headers de segurança
  request.headers.set("X-Frame-Options", "DENY");
  request.headers.set("X-XSS-Protection", "1; mode=block");
  request.headers.set("X-Content-Type-Options", "nosniff");
  request.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");

  return request;
}

function onResponse(response) {
  // 1. Comprimir se não estiver comprimido
  if (!response.headers.get("Content-Encoding")) {
    // response.headers.set("Content-Encoding", "gzip");
  }

  // 2. Adicionar CSP (Content Security Policy) básico
  response.headers.set("Content-Security-Policy", "default-src 'self'");

  return response;
}
