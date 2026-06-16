// functions/large_scale.js
function onTraffic(request) {
    log("Processing transaction payload at edge node.");
    
    // Simular processamento de dados em grande escala
    let data = [];
    for (let i = 0; i < 50; i++) {
        data.push({
            id: i,
            metric: Math.random() * 100,
            status: "active",
            payload_type: "large_scale_telemetry"
        });
    }
    
    request.headers.set('X-Sonic-Data-Processed', data.length.toString());
    request.headers.set('X-Sonic-Engine', 'v1.3.0');
    
    // Gerar um alerta de erro simulado ocasional (5% de chance) para enriquecer a apresentação visual
    if (Math.random() < 0.05) {
        log("[ERROR] Telemetry stream anomaly detected!");
    }
    
    return request;
}

function onResponse(response) {
    response.headers.set('X-Processed-By', 'Sonic-Large-Scale-Edge');
    return response;
}