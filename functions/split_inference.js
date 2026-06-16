// functions/split_inference.js — Sonic Edge Worker for Split Inference
//
// This worker runs intermediate machine learning model layers on the network channel,
// executing directly between the client and the server.
//
// Flow:
// 1. Client runs Layers 0 & 1 (Dense + ReLU) locally and POSTs intermediate activations.
// 2. Sonic intercepts the request in the network channel, executes Layers 2 & 3 (Dense + Softmax).
// 3. Sonic forwards the final classification probabilities to the backend server.

// Define the full neural network model layers
const modelJSON = JSON.stringify({
    name: "split_computing_model",
    layers: [
        {
            // Layer 0: Dense (3 inputs, 2 outputs) - Executed at Client
            type: "dense",
            weights: [
                [0.1, 0.2, 0.3],
                [-0.4, 0.5, 0.6]
            ],
            biases: [0.5, -0.2]
        },
        {
            // Layer 1: ReLU activation - Executed at Client
            type: "relu"
        },
        {
            // Layer 2: Dense (2 inputs, 2 outputs) - Executed at Sonic (Channel)
            type: "dense",
            weights: [
                [0.5, -0.2],
                [0.1, 0.9]
            ],
            biases: [0.0, 0.5]
        },
        {
            // Layer 3: Softmax activation - Executed at Sonic (Channel)
            type: "softmax"
        }
    ]
});

// Load and register the model into the shared registry on script load
try {
    ai.loadModel("split_computing_model", modelJSON);
    log("Split computing model loaded successfully at edge node.");
} catch (e) {
    log("Failed to load model: " + e.message);
}

function onTraffic(request) {
    log("Intercepted split computing request: " + request.method + " " + request.url);

    if (request.method === "POST" && request.body) {
        try {
            const payload = JSON.parse(request.body);
            
            if (payload.activations && Array.isArray(payload.activations)) {
                log("Received intermediate activations from client: " + JSON.stringify(payload.activations));
                
                // Read dynamic model configuration from headers if provided by the client
                const modelName = request.headers.get("X-Model-Name") || "split_computing_model";
                const modelSource = request.headers.get("X-Model-Source");

                if (modelSource) {
                    log("Client requested custom model " + modelName + " from source: " + modelSource);
                    // Dynamically load/download the model (cached automatically if already loaded)
                    ai.loadModel(modelName, modelSource);
                }
                
                // Execute intermediate Layers 2 & 3 in the network channel
                const channelOutput = ai.runLayers(modelName, 2, 3, payload.activations);
                
                log("Executed Layers 2-3 in the network channel. Output: " + JSON.stringify(channelOutput));
                
                // Format response to forward to the server
                payload.activations = channelOutput;
                payload.stage = "channel-processed";
                request.body = JSON.stringify(payload);
                
                // Add header to notify the backend server
                request.headers.set("X-Processed-By-Edge", "true");
                request.headers.set("X-Edge-Computation-Time", Date.now().toString());
            }
        } catch (err) {
            log("Error parsing or executing split model in channel: " + err.message);
        }
    }

    return request;
}

function onResponse(response) {
    response.headers.set("X-Edge-Channel-Inference", "active");
    return response;
}
