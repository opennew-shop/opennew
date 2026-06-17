import express from "express";
import cors from "cors";
import "dotenv/config";
import { UPSTREAM, UPSTREAM_KEY, GATEWAY_API_KEY, MODELS } from "./config.js";
import { selectModels, fusionResponses } from "./fusion.js";

const app = express();
const PORT = process.env.PORT || 3002;

app.use(cors());
app.use(express.json({ limit: "10mb" }));

// ─── Auth middleware ───
function auth(req, res, next) {
  const authz = req.headers["authorization"] || "";
  const token = authz.replace(/^Bearer\s+/i, "").trim();
  if (!token || token !== GATEWAY_API_KEY) {
    return res.status(401).json({ error: "Unauthorized", message: "Invalid API key" });
  }
  next();
}

// ─── Models list ───
app.get("/aggregate/v1/models", (_req, res) => {
  const data = Object.entries(MODELS).map(([id, m]) => ({
    id,
    object: "model",
    created: Math.floor(Date.now() / 1000),
    owned_by: "ancf-gateway",
    description: m.description,
    capabilities: m.capabilities || {},
  }));
  res.json({ object: "list", data });
});

// ─── Chat completions (OpenAI compatible) ───
app.post("/aggregate/v1/chat/completions", auth, async (req, res) => {
  const { model, messages, stream, max_tokens, temperature } = req.body;

  // Resolve which upstream models to call
  const targetModels = model && model !== "ancf-fusion"
    ? [model]
    : selectModels(messages);

  if (!targetModels || targetModels.length === 0) {
    return res.status(400).json({ error: "No suitable models available for this request type" });
  }

  const body = {
    messages,
    max_tokens: max_tokens || 1024,
    temperature: temperature ?? 0.7,
    stream: false,
  };

  // Stream not supported for fusion; if requested, fallback to single model
  // For now, always do non-stream fusion

  try {
    const results = await Promise.allSettled(
      targetModels.map((m) =>
        fetch(`${UPSTREAM}/v1/chat/completions`, {
          method: "POST",
          headers: {
            "Authorization": `Bearer ${UPSTREAM_KEY}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ ...body, model: MODELS[m]?.upstream_model || m }),
        }).then(async (r) => {
          if (!r.ok) {
            const text = await r.text().catch(() => "");
            throw new Error(`Upstream ${m} HTTP ${r.status}: ${text}`);
          }
          return r.json();
        })
      )
    );

    const fused = fusionResponses(results);

    if (fused.error) {
      return res.status(502).json(fused);
    }

    return res.json({
      id: `chatcmpl-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
      object: "chat.completion",
      created: Math.floor(Date.now() / 1000),
      model: "ancf-fusion",
      choices: fused.choices,
      usage: fused.usage,
      _fusion: fused._fusion,
    });
  } catch (e) {
    return res.status(502).json({ error: "Fusion failed", message: e.message });
  }
});

// ─── Health ───
app.get("/aggregate/health", (_req, res) => {
  res.json({ status: "ok", service: "ancf-model-gateway", version: "1.0.0" });
});

app.listen(PORT, "127.0.0.1", () => {
  console.log(`ANCF Model Gateway running on http://127.0.0.1:${PORT}`);
});
