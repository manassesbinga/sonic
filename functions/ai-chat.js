const CF_ACCOUNT_ID = "__CLOUDFLARE_ACCOUNT_ID__";
const CF_API_TOKEN = "__CLOUDFLARE_API_TOKEN__";
const CF_MODEL = "@cf/meta/llama-3.1-8b-instruct-fp8";

function onTraffic(request) {
    if (request.method === "POST" && request.path === "/api/chat") {
        return handleChat(request);
    }
    return request;
}

function handleChat(request) {
    const body = JSON.parse(request.body || "{}");
    const messages = body.messages || [];

    const aiResponse = callCloudflareAI(messages);

    if (aiResponse.error) {
        return new Response(JSON.stringify({ error: aiResponse.error }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
        });
    }

    return new Response(JSON.stringify({ text: aiResponse.text }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
    });
}

function callCloudflareAI(messages) {
    const url = "https://api.cloudflare.com/client/v4/accounts/"
        + CF_ACCOUNT_ID + "/ai/v1/chat/completions";

    const payload = JSON.stringify({
        model: CF_MODEL,
        messages: messages.map(function(m) {
            return { role: m.role === "assistant" ? "assistant" : "user", content: m.content };
        }),
        stream: false,
    });

    const response = fetch(url, {
        method: "POST",
        headers: {
            "Authorization": "Bearer " + CF_API_TOKEN,
            "Content-Type": "application/json",
        },
        body: payload,
    });

    if (response.status !== 200) {
        return { error: "Cloudflare AI error: " + response.status + " " + response.body };
    }

    const data = JSON.parse(response.body);
    const text = data?.result?.response || data?.choices?.[0]?.message?.content || "";
    return { text: text };
}
