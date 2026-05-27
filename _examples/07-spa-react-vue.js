/**
 * EXEMPLO 7: Proxy para SPA (React/Vue/Angular)
 * 
 * Melhora a performance e segurança de apps Single-Page Applications!
 */

function onTraffic(request) {
  const url = new URL(request.url);
  const path = url.pathname;

  // 1. Roteamento para SPA (fallback para index.html)
  const isStaticAsset = 
    path.startsWith("/static/") ||
    path.startsWith("/_next/") ||
    path.includes(".") || // arquivos com extensão (.js, .css, .png, etc.
    path === "/index.html";

  if (!isStaticAsset) {
    // Esta rota é uma rota do SPA - deixar passar para o servidor de frontend
    request.url = url.origin + "/index.html";
    log("SPA route fallback: " + path);
  }

  // 2. Cachear assets staticos por 1 semana
  if (isStaticAsset && (path.endsWith(".js") || path.endsWith(".css") || path.endsWith(".png") || path.endsWith(".jpg")) {
    request.headers.set("Cache-Control", "public, max-age=604800, immutable");
  }

  // 3. Headers de segurança para SPA
  request.headers.set("X-Content-Type-Options", "nosniff");
  request.headers.set("X-Frame-Options", "SAMEORIGIN");

  return request;
}

function onResponse(response) {
  // 1. Remover header X-Powered-By
  response.headers.delete("X-Powered-By");

  // 2. Compressão (se o servidor não fizer)
  if (!response.headers.get("Content-Encoding")) {
    // response.headers.set("Content-Encoding", "gzip"); // somente se comprimir
  }

  return response;
}
