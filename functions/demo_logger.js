// demo_logger.js
function onTraffic(request) {
    console.log("Recebendo conexao simulada no Sonic Proxy para: " + request.url);
    if (request.url.includes("danger") || request.url.includes("exec")) {
        console.warn("ALERTA: Tentativa de exploit suspeita detectada no caminho!");
    } else {
        console.log("Tráfego de requisição padrão processado pelo worker de demonstração.");
    }
    request.headers.set("X-Sonic-Demo", "Active-Simulated-Traffic");
    return request;
}

function onResponse(response) {
    console.log("Processando resposta do upstream no worker de demonstração (Status " + response.status + ")");
    return response;
}
