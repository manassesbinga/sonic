// functions/simple.js
function onTraffic(request) {
    log("Simple worker executing: " + request.url);
    request.headers.set("X-Simple-Worker", "true");
    return request;
}

function onResponse(response) {
    return response;
}
