/**
 * EXEMPLO 4: Logger Estruturado + Métricas no KV
 * 
 * Registra todas as requisições e coleta métricas no KV Store.
 */

function onTraffic(request) {
  const startTime = Date.now();
  const requestId = "req_" + Math.random().toString(36).substr(2, 9);
  
  // Armazenar dados da requisição no KV para o onResponse
  kv.set(`req:${requestId}`, JSON.stringify({
    method: request.method,
    url: request.url,
    startTime: startTime,
    requestId: requestId,
  }));

  request.headers.set("X-Request-ID", requestId);
  return request;
}

function onResponse(response) {
  const requestId = response.headers.get("X-Request-ID");
  const endTime = Date.now();
  
  if (requestId) {
    const reqData = kv.get(`req:${requestId}`);
    if (reqData) {
      const req = JSON.parse(reqData);
      const latency = endTime - req.startTime;

      // Log estruturado
      log(JSON.stringify({
        timestamp: new Date().toISOString(),
        request_id: requestId,
        method: req.method,
        url: req.url,
        status: response.status,
        latency_ms: latency,
      }));

      // Coletar métricas no KV Store
      const hourKey = "metrics:requests:" + new Date().toISOString().slice(0, 13);
      const statusKey = `metrics:status:${response.status}`;
      
      // Incrementar contador de requisições
      let totalReq = parseInt(kv.get(hourKey) || "0");
      kv.set(hourKey, (totalReq + 1).toString());
      
      // Incrementar contador por status
      let statusCount = parseInt(kv.get(statusKey) || "0");
      kv.set(statusKey, (statusCount + 1).toString());
      
      // Acumular latência total
      const latencyKey = "metrics:latency_total:" + new Date().toISOString().slice(0, 13);
      let totalLatency = parseInt(kv.get(latencyKey) || "0");
      kv.set(latencyKey, (totalLatency + latency).toString());

      // Limpar dados temporários
      kv.delete(`req:${requestId}`);
    }
  }

  return response;
}
