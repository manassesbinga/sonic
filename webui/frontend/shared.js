// ==========================================
// Sonic Engine Admin Web UI - Shared Script
// ==========================================

const urlParams = new URLSearchParams(window.location.search);
sessionStorage.removeItem('sonic_design_mode');
let isDesignMode = false;



// Helpers do Mock Database no sessionStorage (Modo Design)
function getMockData(key, defaultValue) {
    const data = sessionStorage.getItem(key);
    if (!data) {
        sessionStorage.setItem(key, JSON.stringify(defaultValue));
        return defaultValue;
    }
    return JSON.parse(data);
}

function saveMockData(key, value) {
    sessionStorage.setItem(key, JSON.stringify(value));
}

// Inicializadores dos dados simulados se estiver em Modo Design
if (isDesignMode) {
    getMockData('mock_status', {
        totalRequests: 84320,
        activeConnections: 12,
        vmPoolSize: 8,
        totalErrors: 0,
        goMemoryAlloc: 2450392,
        goGoroutines: 19,
        ebpfActive: false,
        wslAvailable: true,
        uptimeSeconds: 3600,
        topDomains: [
            { domain: "api.sonic-gateway.io", count: 45201 },
            { domain: "auth.internal", count: 22104 },
            { domain: "static.sonic-gateway.io", count: 12058 },
            { domain: "localhost:9092", count: 4957 }
        ]
    });

    getMockData('mock_workers', [
        {
            name: "auth_validator.js",
            size: "2.4 KB",
            onTraffic: true,
            onResponse: false,
            code: `// Auth Validator Worker\nexport function onTraffic(request) {\n    const auth = request.headers["Authorization"];\n    if (!auth || !auth.startsWith("Bearer ")) {\n        return new Response("Unauthorized", { status: 401 });\n    }\n}`
        },
        {
            name: "response_injector.js",
            size: "1.1 KB",
            onTraffic: false,
            onResponse: true,
            code: `// Response Injector Worker\nexport function onResponse(request, response) {\n    response.headers["X-Powered-By"] = "Sonic-Engine";\n    return response;\n}`
        },
        {
            name: "cache_helper.js",
            size: "890 B",
            onTraffic: true,
            onResponse: false,
            code: `// Cache Helper Worker\nexport function onTraffic(request) {\n    // Custom offline caching logic here\n}`
        }
    ]);

    getMockData('mock_pipelines', [
        {
            id: "pipe-api-validate",
            name: "Validar API Externa",
            pattern: "/api/v1/*",
            methods: ["GET", "POST"],
            steps: [
                { type: "rate_limiter", limit: 100, window: 60000 },
                { type: "worker", name: "auth_validator.js" }
            ],
            enabled: true
        },
        {
            id: "pipe-cve-shield",
            name: "WAF CVE Shield",
            pattern: "/*",
            methods: [],
            steps: [
                { type: "cve_scanner" }
            ],
            enabled: true
        }
    ]);

    getMockData('mock_executions', [
        {
            id: "exec-101",
            timestamp: Date.now() - 2000,
            worker: "auth_validator.js",
            function: "onTraffic",
            method: "GET",
            url: "/api/v1/users/profile",
            duration: "1.2ms",
            status: "success",
            errorMsg: "",
            headers: { "Authorization": "Bearer token123", "Host": "api.sonic-gateway.io" },
            body: "",
            respStatus: 200,
            respHeaders: { "Content-Type": "application/json" },
            respBody: '{"id": 1, "username": "sonic_admin"}'
        },
        {
            id: "exec-102",
            timestamp: Date.now() - 5000,
            worker: "response_injector.js",
            function: "onResponse",
            method: "POST",
            url: "/api/v1/auth/login",
            duration: "400µs",
            status: "success",
            errorMsg: "",
            headers: { "Host": "api.sonic-gateway.io" },
            body: '{"username": "user", "password": "pwd"}',
            respStatus: 200,
            respHeaders: { "X-Powered-By": "Sonic-Engine", "Content-Type": "application/json" },
            respBody: '{"token": "xyz123"}'
        },
        {
            id: "exec-103",
            timestamp: Date.now() - 15000,
            worker: "auth_validator.js",
            function: "onTraffic",
            method: "GET",
            url: "/api/v1/admin/debug",
            duration: "850µs",
            status: "error",
            errorMsg: "Unauthorized access: Bearer token is missing",
            headers: { "Host": "api.sonic-gateway.io" },
            body: "",
            respStatus: 401,
            respHeaders: { "Content-Type": "text/plain" },
            respBody: "Unauthorized"
        }
    ]);

    getMockData('mock_logs', [
        { timestamp: Date.now() - 30000, level: "INFO", message: "Sonic Edge Engine v1.4.0 starting..." },
        { timestamp: Date.now() - 28000, level: "INFO", message: "Loading config file: sonic_test.yaml" },
        { timestamp: Date.now() - 25000, level: "INFO", message: "Kernel Socksmap support verified (eBPF emulation active)" },
        { timestamp: Date.now() - 20000, level: "INFO", message: "Admin interface listening on http://localhost:9092" },
        { timestamp: Date.now() - 10000, level: "INFO", message: "Loaded 3 edge workers successfully" },
        { timestamp: Date.now() - 5000, level: "WARN", message: "High latency detected on upstream server: internal-api (142ms)" }
    ]);

    getMockData('mock_cves', [
        {
            id: "cve-alert-001",
            timestamp: Date.now() - 45000,
            signature: "SQL Injection check",
            preview: "GET /api/v1/products?id=1%20union%20select%20null,username,password%20from%20users\nUser-Agent: sqlmap/1.4\nHost: api.sonic-gateway.io"
        },
        {
            id: "cve-alert-002",
            timestamp: Date.now() - 120000,
            signature: "Cross-Site Scripting (XSS)",
            preview: "POST /api/v1/comments\nContent-Type: application/json\n\n{\"text\": \"<script>alert('xss')</script>\"}"
        }
    ]);

    getMockData('mock_ai_stats', {
        totalRequests: 1420,
        cacheHits: 890,
        tokensSaved: 425100,
        costSavedUSD: 4.25
    });

    getMockData('mock_ai_cache', [
        { prompt: "What is the capital of France?", response: "The capital of France is Paris.", timestamp: Date.now() - 10000 },
        { prompt: "How do I reverse a list in Python?", response: "You can reverse a list in Python using list.reverse() or slicing: list[::-1].", timestamp: Date.now() - 40000 }
    ]);

    getMockData('mock_ai_config', {
        active: true,
        mode: "embeddings",
        threshold: 0.85,
        openAIKey: "sk-proj-LL27d91vFk29sXhPzQp1",
        anthropicKey: "sk-ant-sid01-OPX98asdfm",
        defaultProvider: "openai",
        fallbackProvider: "anthropic"
    });

    getMockData('mock_sandbox_config', {
        active: true,
        enabledDomains: ["api.sonic-gateway.io", "localhost:9092"]
    });

    getMockData('mock_sandbox_active', []);

    // Interceptador Global do fetch para rodar offline
    window.fetch = async function(url, options) {
        await new Promise(resolve => setTimeout(resolve, 120));
        const urlObj = new URL(url, window.location.origin);
        const pathName = urlObj.pathname;
        const method = (options && options.method || 'GET').toUpperCase();
        
        let responseData = null;
        let responseStatus = 200;
        
        if (pathName === '/api/status') {
            const status = getMockData('mock_status');
            status.totalRequests += Math.floor(Math.random() * 3);
            status.uptimeSeconds += 3;
            if (Math.random() > 0.8) {
                status.activeConnections = Math.max(2, status.activeConnections + (Math.random() > 0.5 ? 1 : -1));
            }
            saveMockData('mock_status', status);
            responseData = status;
        } 
        else if (pathName === '/api/executions') {
            const execs = getMockData('mock_executions');
            // Simular ocasionalmente novas execuções
            if (Math.random() > 0.7) {
                const workers = getMockData('mock_workers');
                const randomWorker = workers.length > 0 ? workers[Math.floor(Math.random() * workers.length)].name : "worker.js";
                const isErr = Math.random() > 0.95;
                const newExec = {
                    id: "exec-" + Math.floor(10000 + Math.random() * 90000),
                    timestamp: Date.now(),
                    worker: randomWorker,
                    function: Math.random() > 0.5 ? "onTraffic" : "onResponse",
                    method: ["GET", "POST", "PUT"][Math.floor(Math.random() * 3)],
                    url: ["/api/v1/users", "/api/v1/auth", "/products", "/checkout"][Math.floor(Math.random() * 4)],
                    duration: (Math.random() * 3 + 0.1).toFixed(1) + "ms",
                    status: isErr ? "error" : "success",
                    errorMsg: isErr ? "Internal error processing function" : "",
                    headers: { "Host": "api.sonic-gateway.io", "User-Agent": "Mozilla/5.0" },
                    body: "",
                    respStatus: isErr ? 500 : 200,
                    respHeaders: { "Content-Type": "application/json" },
                    respBody: isErr ? "Error" : '{"status": "ok"}'
                };
                if (isErr) {
                    const status = getMockData('mock_status');
                    status.totalErrors += 1;
                    saveMockData('mock_status', status);
                }
                execs.unshift(newExec);
                if (execs.length > 50) execs.pop();
                saveMockData('mock_executions', execs);
            }
            responseData = execs;
        } 
        else if (pathName === '/api/logs') {
            const logs = getMockData('mock_logs');
            if (Math.random() > 0.6) {
                logs.push({
                    timestamp: Date.now(),
                    level: ["INFO", "WARN", "ERROR"][Math.floor(Math.random() * 3)],
                    message: "Request processed successfully on route fallback"
                });
                if (logs.length > 100) logs.shift();
                saveMockData('mock_logs', logs);
            }
            responseData = logs;
        } 
        else if (pathName === '/api/cves') {
            const cves = getMockData('mock_cves');
            if (Math.random() > 0.96) {
                cves.unshift({
                    id: "cve-alert-" + Math.floor(Math.random() * 1000),
                    timestamp: Date.now(),
                    signature: "SQL Injection Exploit",
                    preview: "GET /api/v1/items?search=1' OR '1'='1"
                });
                if (cves.length > 20) cves.pop();
                saveMockData('mock_cves', cves);
            }
            responseData = cves;
        } 
        else if (pathName === '/api/workers') {
            let workers = getMockData('mock_workers');
            if (method === 'GET') {
                const name = urlObj.searchParams.get('name');
                if (name) {
                    const w = workers.find(x => x.name === name);
                    responseData = w ? w : { name, code: "// Worker not found" };
                } else {
                    responseData = workers;
                }
            } else if (method === 'POST') {
                const req = JSON.parse(options.body);
                const idx = workers.findIndex(x => x.name === req.name);
                if (idx !== -1) {
                    workers[idx].code = req.code;
                    workers[idx].size = (req.code.length / 1024).toFixed(1) + " KB";
                } else {
                    workers.push({
                        name: req.name,
                        size: (req.code.length / 1024).toFixed(1) + " KB",
                        onTraffic: req.code.includes("onTraffic"),
                        onResponse: req.code.includes("onResponse"),
                        code: req.code
                    });
                }
                saveMockData('mock_workers', workers);
                responseData = { status: "saved" };
            } else if (method === 'DELETE') {
                const name = urlObj.searchParams.get('name');
                workers = workers.filter(x => x.name !== name);
                saveMockData('mock_workers', workers);
                responseData = { status: "deleted" };
            }
        } 
        else if (pathName === '/api/pipelines') {
            let pipelines = getMockData('mock_pipelines');
            if (method === 'GET') {
                responseData = pipelines;
            } else if (method === 'POST') {
                const req = JSON.parse(options.body);
                const idx = pipelines.findIndex(x => x.id === req.id);
                if (idx !== -1) {
                    pipelines[idx] = req;
                } else {
                    pipelines.push(req);
                }
                saveMockData('mock_pipelines', pipelines);
                responseData = { status: "saved" };
            } else if (method === 'DELETE') {
                const id = urlObj.searchParams.get('id');
                pipelines = pipelines.filter(x => x.id !== id);
                saveMockData('mock_pipelines', pipelines);
                responseData = { status: "deleted" };
            }
        } 
        else if (pathName === '/api/sandbox/config') {
            if (method === 'POST') {
                const req = JSON.parse(options.body);
                saveMockData('mock_sandbox_config', req);
                responseData = { status: "ok" };
            } else {
                responseData = getMockData('mock_sandbox_config');
            }
        } 
        else if (pathName === '/api/sandbox/active') {
            responseData = getMockData('mock_sandbox_active');
        } 
        else if (pathName === '/api/sandbox/resume') {
            const req = JSON.parse(options.body);
            let active = getMockData('mock_sandbox_active');
            active = active.filter(x => x.id !== req.id);
            saveMockData('mock_sandbox_active', active);
            responseData = { status: "ok" };
        } 
        else if (pathName === '/api/ai/stats') {
            const stats = getMockData('mock_ai_stats');
            if (Math.random() > 0.8) {
                stats.totalRequests += 1;
                if (Math.random() > 0.5) {
                    stats.cacheHits += 1;
                    stats.tokensSaved += Math.floor(Math.random() * 200 + 50);
                    stats.costSavedUSD += 0.0015;
                }
                saveMockData('mock_ai_stats', stats);
            }
            responseData = stats;
        } 
        else if (pathName === '/api/ai/config') {
            if (method === 'POST') {
                const req = JSON.parse(options.body);
                saveMockData('mock_ai_config', req);
                responseData = { status: "ok" };
            } else {
                responseData = getMockData('mock_ai_config');
            }
        } 
        else if (pathName === '/api/ai/cache') {
            responseData = getMockData('mock_ai_cache');
        } 
        else if (pathName === '/api/ai/clear-cache') {
            saveMockData('mock_ai_cache', []);
            const stats = getMockData('mock_ai_stats');
            stats.cacheHits = 0;
            saveMockData('mock_ai_stats', stats);
            responseData = { status: "ok" };
        } 
        else if (pathName === '/api/audit') {
            responseData = getMockData('mock_executions');
        } 
        else if (pathName === '/api/test/generate-traffic') {
            const status = getMockData('mock_status');
            status.totalRequests += 500;
            saveMockData('mock_status', status);
            responseData = { status: "ok" };
        } 
        else {
            responseData = { status: "ok" };
        }
        
        return {
            ok: true,
            status: responseStatus,
            json: async () => responseData,
            text: async () => JSON.stringify(responseData)
        };
    };
}

// Variáveis Globais de Estado
window.isWSLInstalled = false;
window.iseBPFActive = false;
window.isDesignMode = isDesignMode;

// Helpers Globais
window.escapeHTML = function(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;')
              .replace(/</g, '&lt;')
              .replace(/>/g, '&gt;')
              .replace(/"/g, '&quot;')
              .replace(/'/g, '&#039;');
};

window.getHeaders = function() {
    const token = sessionStorage.getItem('sonic_token') || '';
    return {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/json'
    };
};

window.fetchData = async function(url) {
    try {
        const res = await fetch(url, {
            headers: window.getHeaders()
        });
        if (res.status === 401) {
            window.logout();
            return null;
        }
        if (!res.ok) throw new Error("API request failed");
        return await res.json();
    } catch (err) {
        console.error("Fetch error:", err);
        return null;
    }
};

window.logout = function() {
    sessionStorage.removeItem('sonic_token');
    sessionStorage.removeItem('sonic_design_mode');
    window.location.reload();
};

window.shutdownServer = async function() {
    if (confirm("Deseja realmente desligar o Sonic Engine?")) {
        if (window.isDesignMode) {
            alert("Servidor desligado (Simulado no Modo Design)");
            window.logout();
            return;
        }
        try {
            await fetch('/api/shutdown', {
                method: 'POST',
                headers: window.getHeaders()
            });
            alert("Sinal de desligamento enviado.");
            window.logout();
        } catch (e) {
            alert("Erro ao enviar comando de desligamento.");
        }
    }
};

window.toggleMobileMenu = function() {
    const sidebar = document.getElementById('app-sidebar');
    const overlay = document.getElementById('sidebar-overlay');
    if (sidebar && overlay) {
        sidebar.classList.toggle('open');
        overlay.classList.toggle('open');
    }
};

// Determina qual aba está ativa baseado no nome do ficheiro HTML
function getActivePageName() {
    const path = window.location.pathname;
    if (path.endsWith('workers.html')) return 'workers';
    if (path.endsWith('pipelines.html')) return 'pipelines';
    if (path.endsWith('sandbox.html')) return 'sandbox';
    if (path.endsWith('aigateway.html')) return 'aigateway';
    if (path.endsWith('executions.html')) return 'executions';
    if (path.endsWith('logs.html')) return 'logs';
    if (path.endsWith('audit.html')) return 'audit';
    if (path.endsWith('security.html')) return 'security';
    if (path.endsWith('protocols.html')) return 'protocols';
    return 'dashboard'; // Default para index.html ou "/"
}

// Injeção de componentes comuns
document.addEventListener('DOMContentLoaded', () => {
    const activePage = getActivePageName();
    
    // 1. Criar tela de autenticação se não existir
    let authScreen = document.getElementById('auth-screen');
    if (!authScreen) {
        authScreen = document.createElement('div');
        authScreen.id = 'auth-screen';
        authScreen.innerHTML = `
            <div class="auth-card">
                <div class="auth-logo">SONIC EDGE ENGINE</div>
                <div class="auth-title">Acesso Administrativo Requerido</div>
                <form id="auth-form" onsubmit="window.handleAuthSubmit(event)">
                    <input type="password" id="token-input" class="auth-input" placeholder="Digite o Token de Acesso" required autocomplete="off">
                    <button type="submit" class="auth-button">Desbloquear Painel</button>
                    <div id="auth-error" class="error-message">Token inválido ou não autorizado</div>
                </form>
            </div>
        `;
        document.body.appendChild(authScreen);
    }
    
    // 2. Criar modal do WSL se não existir
    let wslModal = document.getElementById('wsl-modal');
    if (!wslModal) {
        wslModal = document.createElement('div');
        wslModal.id = 'wsl-modal';
        wslModal.style.display = 'none';
        wslModal.style.position = 'fixed';
        wslModal.style.top = '0';
        wslModal.style.left = '0';
        wslModal.style.width = '100%';
        wslModal.style.height = '100%';
        wslModal.style.backgroundColor = 'rgba(0, 0, 0, 0.85)';
        wslModal.style.backdropFilter = 'blur(5px)';
        wslModal.style.zIndex = '2000';
        wslModal.style.justifyContent = 'center';
        wslModal.style.alignItems = 'center';
        wslModal.style.opacity = '0';
        wslModal.style.transition = 'opacity 0.3s ease';
        wslModal.innerHTML = `
            <div class="auth-card" style="max-width: 650px; width: 90%; max-height: 90vh; overflow-y: auto; background-color: #09090b; border: 1px solid var(--border-color); padding: 2rem; position: relative;">
                <div style="display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid var(--border-color); padding-bottom: 1rem; margin-bottom: 1.5rem;">
                    <h3 style="font-family: 'Outfit'; font-size: 1.3rem; color: #ffffff; display: flex; align-items: center; gap: 0.5rem;">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" style="color: #ffffff;"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
                        Aceleração eBPF via WSL 2
                    </h3>
                    <button onclick="window.closeWSLModal()" style="background: transparent; border: none; color: var(--text-muted); cursor: pointer; display: flex; align-items: center; justify-content: center; font-size: 1.5rem; outline: none;">&times;</button>
                </div>
                
                <div style="display: flex; flex-direction: column; gap: 1.25rem; font-size: 0.9rem; line-height: 1.6;">
                    <p style="color: var(--text-muted);">
                        Para habilitar o bypass de rede ultra rápido via eBPF e Sockmap, o Sonic Engine precisa rodar em um Kernel Linux moderno. Você pode configurar isso facilmente no Windows rodando o engine sob o WSL 2.
                    </p>
                    
                    <div id="wsl-status-info" style="border: 1px dashed var(--border-color); padding: 0.75rem 1rem; border-radius: 6px; background-color: #040406; font-size: 0.85rem;">
                    </div>

                    <div>
                        <h4 style="font-family: 'Outfit'; color: #ffffff; margin-bottom: 0.5rem; font-size: 0.95rem;">Passo a Passo de Instalação e Execução:</h4>
                        <ol style="margin-left: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; color: var(--text-muted);">
                            <li>
                                <strong style="color: #ffffff; font-family: 'Outfit';">Instale o WSL 2 e uma distro Linux (ex: Ubuntu) se ainda não tiver:</strong>
                                <p style="margin-top: 0.15rem; font-size: 0.85rem;">Abra o PowerShell como Administrador e execute:</p>
                                <pre style="background-color: #000000; color: #ffffff; padding: 0.5rem 0.75rem; border: 1px solid var(--border-color); border-radius: 5px; font-family: monospace; font-size: 0.8rem; margin-top: 0.25rem; white-space: pre-wrap; word-break: break-all;">wsl --install</pre>
                            </li>
                            <li>
                                <strong style="color: #ffffff; font-family: 'Outfit';">Atualize o WSL para obter o Kernel mais recente:</strong>
                                <pre style="background-color: #000000; color: #ffffff; padding: 0.5rem 0.75rem; border: 1px solid var(--border-color); border-radius: 5px; font-family: monospace; font-size: 0.8rem; margin-top: 0.25rem; white-space: pre-wrap; word-break: break-all;">wsl --update</pre>
                            </li>
                            <li>
                                <strong style="color: #ffffff; font-family: 'Outfit';">Compile e execute o Sonic Engine no ambiente Linux:</strong>
                                <p style="margin-top: 0.15rem; font-size: 0.85rem;">Clone o repositório do Sonic dentro do WSL, instale as dependências (Go >= 1.21) e execute com privilégios de root para que ele possa interagir com o subsistema eBPF:</p>
                                <pre style="background-color: #000000; color: #ffffff; padding: 0.5rem 0.75rem; border: 1px solid var(--border-color); border-radius: 5px; font-family: monospace; font-size: 0.8rem; margin-top: 0.25rem; white-space: pre-wrap; word-break: break-all;">go build -o sonic cmd/main.go\nsudo ./sonic --config config.yaml</pre>
                            </li>
                        </ol>
                    </div>
                </div>
                
                <div style="margin-top: 2rem; display: flex; justify-content: flex-end;">
                    <button onclick="window.closeWSLModal()" class="auth-button" style="width: auto; padding: 0.6rem 1.5rem; font-family: 'Outfit';">Entendido</button>
                </div>
            </div>
        `;
        document.body.appendChild(wslModal);
    }
    
    // 3. Criar header móvel
    const mobileHeader = document.createElement('header');
    mobileHeader.className = 'mobile-header';
    mobileHeader.innerHTML = `
        <button class="hamburger-btn" onclick="window.toggleMobileMenu()">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="18" x2="21" y2="18"/></svg>
        </button>
        <div class="mobile-logo">SONIC</div>
        <span class="badge" style="font-size: 0.65rem;">Active</span>
    `;
    
    // 4. Criar estrutura de layout principal (sidebar + content wrapper + footer)
    const appLayout = document.createElement('div');
    appLayout.className = 'app-layout';
    
    const sidebar = document.createElement('aside');
    sidebar.className = 'sidebar';
    sidebar.id = 'app-sidebar';
    sidebar.innerHTML = `
        <div class="sidebar-brand">
            <div class="brand-logo">SONIC</div>
            <span class="badge">Active</span>
        </div>
        
        <nav class="sidebar-nav">
            <a href="index.html" id="btn-tab-dashboard" class="tab-btn ${activePage === 'dashboard' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="9"/><rect x="14" y="3" width="7" height="5"/><rect x="14" y="12" width="7" height="9"/><rect x="3" y="16" width="7" height="5"/></svg>
                Dashboard
            </a>
            <a href="workers.html" id="btn-tab-workers" class="tab-btn ${activePage === 'workers' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2H2v10h10V2zM22 12H12v10h10V12zM12 7h10M2 17h10"/></svg>
                Edge Workers
            </a>
            <a href="pipelines.html" id="btn-tab-pipelines" class="tab-btn ${activePage === 'pipelines' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>
                Pipelines
            </a>
            <a href="sandbox.html" id="btn-tab-sandbox" class="tab-btn ${activePage === 'sandbox' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><line x1="9" y1="9" x2="15" y2="15"/><line x1="15" y1="9" x2="9" y2="15"/></svg>
                Live Sandbox
            </a>
            <a href="aigateway.html" id="btn-tab-aigateway" class="tab-btn ${activePage === 'aigateway' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z"/><path d="M12 6v6l4 2"/></svg>
                AI Gateway
            </a>
            <a href="executions.html" id="btn-tab-executions" class="tab-btn ${activePage === 'executions' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
                Executions Log
            </a>
            <a href="logs.html" id="btn-tab-logs" class="tab-btn ${activePage === 'logs' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M18 10h-1.25A2.75 2.75 0 0 0 14 12.75v5A2.75 2.75 0 0 0 16.75 20.5H18M6 10h1.25A2.75 2.75 0 0 1 10 12.75v5A2.75 2.75 0 0 1 7.25 20.5H6"/></svg>
                Console Output
            </a>
            <a href="audit.html" id="btn-tab-audit" class="tab-btn ${activePage === 'audit' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
                Auditoria
            </a>
            <a href="security.html" id="btn-tab-security" class="tab-btn ${activePage === 'security' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
                CVE Scanner
            </a>
            <a href="protocols.html" id="btn-tab-protocols" class="tab-btn ${activePage === 'protocols' ? 'active' : ''}">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>
                Protocols
            </a>
        </nav>
        
        <div id="ebpf-sidebar-status" style="padding: 0.75rem 1.25rem; border-top: 1px solid var(--border-color); font-size: 0.8rem; display: flex; align-items: center; justify-content: space-between; background-color: #060608;">
            <span style="color: var(--text-muted); font-family: 'Outfit';">eBPF Active:</span>
            <span id="ebpf-sidebar-badge" class="status-badge warning" style="font-size: 0.65rem; padding: 0.15rem 0.4rem; cursor: pointer;" onclick="window.showWSLInstructionModal(event)">Unknown</span>
        </div>

        <div class="sidebar-footer">
            <div id="uptime-display" class="uptime-text">Uptime: -</div>
            <div class="footer-actions">
                <button class="btn-sidebar-action" onclick="window.logout()">Trancar</button>
                <button class="btn-sidebar-action btn-danger" onclick="window.shutdownServer()">Desligar</button>
            </div>
            <div class="version-text">Sonic Engine v1.4.0</div>
        </div>
    `;

    const overlay = document.createElement('div');
    overlay.className = 'sidebar-overlay';
    overlay.id = 'sidebar-overlay';
    overlay.onclick = window.toggleMobileMenu;

    const contentWrapper = document.createElement('div');
    contentWrapper.className = 'content-wrapper';
    
    const main = document.createElement('main');
    
    // Mover os nós filhos originais (não scripts e não os criados por nós) do body para <main>
    const bodyChildren = Array.from(document.body.childNodes);
    bodyChildren.forEach(child => {
        if (child !== authScreen && child !== wslModal) {
            // Manter tags <script> e <link> diretamente anexadas no body para garantir execução
            if (child.nodeType === Node.ELEMENT_NODE && (child.tagName === 'SCRIPT' || child.tagName === 'LINK')) {
                // Manter no body
            } else {
                main.appendChild(child);
            }
        }
    });

    const footer = document.createElement('footer');
    footer.innerHTML = `<p>Sonic Engine v1.4.0 — Multi-Language Edge Computing Proxy</p>`;

    contentWrapper.appendChild(main);
    contentWrapper.appendChild(footer);

    appLayout.appendChild(sidebar);
    appLayout.appendChild(overlay);
    appLayout.appendChild(contentWrapper);

    document.body.appendChild(mobileHeader);
    document.body.appendChild(appLayout);

    // Validação da autenticação para carregar dados
    window.checkAuthentication();
});

// Funções de login/auth
// Modo Design removido

window.handleAuthSubmit = function(e) {
    e.preventDefault();
    const tokenVal = document.getElementById('token-input').value.trim();
    if (tokenVal) {
        sessionStorage.setItem('sonic_token', tokenVal);
        window.fetchData('/api/status').then(data => {
            if (data) {
                window.checkAuthentication();
            } else {
                const err = document.getElementById('auth-error');
                if (err) err.style.display = 'block';
                sessionStorage.removeItem('sonic_token');
            }
        });
    }
};

window.checkAuthentication = function() {
    const token = sessionStorage.getItem('sonic_token');
    const authScreen = document.getElementById('auth-screen');
    
    if (token) {
        
        if (authScreen) {
            authScreen.style.opacity = '0';
            setTimeout(() => {
                authScreen.style.display = 'none';
            }, 300);
        }
        
        // Acionar inicialização da página específica
        if (typeof window.onPageInit === 'function') {
            window.onPageInit();
        }
        
        window.startPolling();
    } else {
        if (authScreen) {
            authScreen.style.display = 'flex';
            authScreen.style.opacity = '1';
        }
    }
};

// Polling System
let pollingTimer = null;

window.startPolling = function() {
    window.pollImmediate();
    if (!pollingTimer) {
        pollingTimer = setInterval(window.poll, 3000);
    }
};

window.pollImmediate = async function() {
    await window.poll();
};

window.poll = async function() {
    // 1. Polling comum do sidebar
    await pollSidebar();
    
    // 2. Polling específico da página ativa
    if (typeof window.onPagePoll === 'function') {
        try {
            await window.onPagePoll();
        } catch (e) {
            console.error("Erro no polling da página:", e);
        }
    }
};

async function pollSidebar() {
    const data = await window.fetchData('/api/status');
    if (data) {
        updateUptimeDisplay(data.uptimeSeconds);
        updateEBPFStatus(data.ebpfActive, data.wslAvailable);
    }
}

function updateUptimeDisplay(seconds) {
    const el = document.getElementById('uptime-display');
    if (!el) return;
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    el.innerText = `Uptime: ${String(h).padStart(2,'0')}:${String(m).padStart(2,'0')}:${String(s).padStart(2,'0')}`;
}

function updateEBPFStatus(active, wslAvailable) {
    window.iseBPFActive = active;
    window.isWSLInstalled = wslAvailable;
    const badge = document.getElementById('ebpf-sidebar-badge');
    if (!badge) return;
    
    if (active) {
        badge.innerText = "Kernel Active";
        badge.className = "status-badge success";
        badge.style.borderStyle = "solid";
        badge.style.backgroundColor = "#ffffff";
        badge.style.color = "#000000";
        badge.style.borderColor = "#ffffff";
    } else {
        badge.innerText = "User Fallback";
        badge.className = "status-badge warning";
        badge.style.borderStyle = "dashed";
        badge.style.backgroundColor = "transparent";
        badge.style.color = "var(--text-muted)";
        badge.style.borderColor = "var(--border-color)";
    }
}

// WSL 2 Setup Instructions Modal Controls
window.showWSLInstructionModal = function(e) {
    if (e) e.preventDefault();
    if (window.iseBPFActive) return;

    const modal = document.getElementById('wsl-modal');
    const statusInfo = document.getElementById('wsl-status-info');

    if (statusInfo) {
        if (window.isWSLInstalled) {
            statusInfo.innerHTML = `
                <div style="display: flex; gap: 0.5rem; align-items: flex-start; color: #ff9f0a; line-height: 1.4;">
                    <span style="display: inline-block; width: 8px; height: 8px; background-color: #ff9f0a; border-radius: 50%; margin-top: 0.35rem; flex-shrink: 0;"></span>
                    <div><strong>Atenção:</strong> WSL 2 detectado no host, mas a aceleração eBPF do Sonic Engine está <strong>Desativada</strong> porque você iniciou o binário de forma nativa no Windows. O eBPF funciona exclusivamente de dentro do Linux. Siga o Passo 3 abaixo para executar corretamente no terminal do WSL 2.</div>
                </div>
            `;
            statusInfo.style.borderColor = '#ff9f0a';
        } else {
            statusInfo.innerHTML = `
                <div style="display: flex; gap: 0.5rem; align-items: flex-start; color: #ef4444; line-height: 1.4;">
                    <span style="display: inline-block; width: 8px; height: 8px; background-color: #ef4444; border-radius: 50%; margin-top: 0.35rem; flex-shrink: 0;"></span>
                    <div><strong>eBPF Inativo:</strong> WSL 2 não está ativo ou instalado nesta máquina. A aceleração de rede em nível de kernel (eBPF/Sockmap) só está disponível rodando em Linux nativo ou dentro do WSL 2 habilitado.</div>
                </div>
            `;
            statusInfo.style.borderColor = '#ef4444';
        }
    }

    if (modal) {
        modal.style.display = 'flex';
        setTimeout(() => {
            modal.style.opacity = '1';
        }, 10);
    }
};

window.closeWSLModal = function() {
    const modal = document.getElementById('wsl-modal');
    if (modal) {
        modal.style.opacity = '0';
        setTimeout(() => {
            modal.style.display = 'none';
        }, 300);
    }
};
