# Exemplos de Workers Sonic 🚀

Pasta com exemplos práticos de workers que demonstram o poder da nova arquitetura Sonic!

## Como usar
Copie os arquivos `.js` para a pasta `functions/` do seu projeto Sonic e execute `sonic dev` ou `sonic start`!

---

## 📂 Exemplos por Framework/Projeto

### 🚀 Next.js
#### 5. [Proxy para API Routes](05-nextjs-api-proxy.js)
- Rate limiting para rotas `/api/*`
- Headers CORS e de segurança
- Cache de respostas

#### 6. [Autenticação Centralizada](06-nextjs-auth.js)
- Protege páginas e APIs sem modificar o código Next.js
- Rotas públicas configuráveis
- Redirecionamento para login
- Headers de usuário para o app

### ⚛️ React/Vue/Angular (SPA)
#### 7. [Proxy para SPA](07-spa-react-vue.js)
- Fallback para `index.html` (roteamento SPA)
- Cache de assets estáticos por 1 semana
- Headers de segurança

### 🟢 Node.js Backend (Express/NestJS/Fastify)
#### 8. [Proxy para Backend Node.js](08-nodejs-backend.js)
- Rate limiting global
- Sanitização de inputs contra XSS/SQLi
- Tracing com X-Request-ID
- Log de performance

### 📄 Site Estático (Hugo/Jekyll/Gatsby/HTML puro)
#### 9. [Proxy para Site Estático](09-static-site.js)
- Cache inteligente (1 ano para assets, 5min para HTML)
- Redirecionamento www → non-www
- Headers CSP e de segurança

---

## 📂 Exemplos Genéricos

### 1. [Rate Limiter](01-rate-limiter.js)
Limita cada IP a 100 requisições por minuto usando o KV Store persistente.
- **Uso**: Protege APIs de abusos
- **Features**: Headers informativos (X-RateLimit-*), resposta 429 com Retry-After

### 2. [JWT Auth + Cache](02-jwt-auth.js)
Valida tokens JWT e cacheia resultados no KV para performance máxima.
- **Uso**: Autenticação de APIs
- **Features**: Rotas públicas, cache por token, headers X-User-*

### 3. [Transformação](03-transform.js)
Modifica requests e responses em tempo real.
- **Uso**: Migração de APIs, adição de headers de segurança
- **Features**: Reescrita de URL, headers de segurança, tracing, métricas de latência

### 4. [Logger Estruturado](04-structured-logger.js)
Registra todas as requisições e coleta métricas no KV Store.
- **Uso**: Observabilidade, analytics
- **Features**: Log JSON, métricas por hora, contadores de status

---

## 💡 Ideias de Projetos Incríveis que Você Pode Criar

### 🛡️ WAF (Web Application Firewall)
Combine rate limiting + validação de payloads + bloqueio de IPs maliciosos.
```javascript
// Exemplo: Bloquear SQL Injection
function onTraffic(req) {
  const payload = req.body.toLowerCase();
  if (payload.includes("union select") || payload.includes("drop table")) {
    kv.set(`block:${req.headers.get("X-Real-IP")}`, "1");
    return new Response("Forbidden", { status: 403 });
  }
  return req;
}
```

### 🔄 Gateway de API Unificado
Combine múltiplos microsserviços em uma única API.
- Reescrever URLs (`/api/users/` → `http://user-service:3000/`)
- Autenticação centralizada
- Rate limiting por endpoint
- Cache de respostas

### 📊 Analytics em Tempo Real
Coletar métricas de todas as requisições e exibir dashboards.
- Contadores por endpoint
- Latência média
- Taxa de erros
- Top IPs, User-Agents, etc.

### 🎭 Mock Server para Desenvolvimento
Retornar respostas mockadas para desenvolvimento sem depender de serviços externos.
```javascript
function onTraffic(req) {
  if (req.url.includes("/api/mock/")) {
    return new Response(JSON.stringify({ 
      id: 123, 
      name: "Mock User",
      timestamp: Date.now()
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" }
    });
  }
  return req;
}
```

### 🌍 Geolocalização e Bloqueio por País
Usar IP para bloquear/redirecionar usuários por localização geográfica.

### 🔐 Single Sign-On (SSO) Proxy
Centralizar autenticação para múltiplos apps com OAuth2, SAML, etc.

---

## 🔮 Auto-Compilação WASM (Programador Não Precisa Compilar!)

O Sonic faz tudo automaticamente! Você só escreve o código na linguagem que quiser — o Sonic compila para WASM sozinho! 🚀

### Como funciona?
1. Você cria um arquivo `rate-limiter.rs` (Rust) ou `auth.go` (Go) na pasta `functions/`
2. Você executa `sonic dev` ou `sonic start`
3. **O Sonic detecta automaticamente, compila para WASM e executa!**

### Exemplos de Código (Prontos para Usar!)

#### 10. [Worker em Rust](10-rust-worker.rs)
Escreva rate limiting ultra-rápido em Rust — o Sonic compila para WASM!

#### 11. [Worker em Go](11-go-worker.go)
Use Go para workers — compilação automática!

---

## 🔮 Próximos Passos
Quando o suporte a WebAssembly estiver 100% pronto:
- Suporte completo a **Rust**, **Go**, **C**, **AssemblyScript**
- Compartilhar estado entre workers de diferentes linguagens via KV Store!

🚀 O céu é o limite!
