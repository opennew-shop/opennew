import { MODELS, HAS_VISION } from "./config.js";

/**
 * Check if user messages contain images (vision input)
 */
export function hasImageRequest(messages) {
  for (const m of messages || []) {
    const content = m.content || [];
    if (Array.isArray(content)) {
      for (const part of content) {
        if (part.type === "image_url") return true;
      }
    }
  }
  return false;
}

/**
 * Determine which sub-models to call based on input type
 */
export function selectModels(messages) {
  const hasImage = hasImageRequest(messages);
  if (hasImage) {
    // Only multimodal models can handle images
    return HAS_VISION.filter((m) => MODELS[m]);
  }
  // Text only — call all sub-models
  return Object.keys(MODELS).filter((m) => m !== "ancf-fusion");
}

/**
 * Extract content from upstream response handling null content + reasoning
 */
function extractContent(msg) {
  // Some models return content: null with reasoning field populated
  const reasoning = msg.reasoning || msg.reasoning_content || "";
  let content = msg.content || "";
  // If content is null/empty but reasoning exists, use reasoning as content
  if (!content && reasoning) content = reasoning;
  return { content, reasoning };
}

/**
 * Fusion strategy: collect all parallel responses and merge
 */
export function fusionResponses(results) {
  const fulfilled = results.filter((r) => r.status === "fulfilled" && r.value?.choices?.[0]);

  if (fulfilled.length === 0) {
    const errors = results.filter((r) => r.reason).map((r) => String(r.reason));
    return {
      error: { message: errors.join("; "), type: "upstream_error" },
      choices: [],
    };
  }

  const scored = fulfilled.map((r) => {
    const msg = r.value.choices[0].message || {};
    const { content, reasoning } = extractContent(msg);
    const score = (content || "").length + (reasoning || "").length;
    return {
      response: r.value,
      score,
      model: r.value.model || "unknown",
      content: content || "",
      reasoning: reasoning || "",
    };
  });

  // Sort by score descending — best answer wins
  scored.sort((a, b) => b.score - a.score);
  const best = scored[0];

  // Build fused reasoning trace from all models
  let fusedReasoning = "";
  for (const s of scored) {
    if (s.reasoning && s.model !== best.model) {
      fusedReasoning += `[${s.model}]: ${s.reasoning}\n`;
    }
  }

  return {
    choices: [
      {
        finish_reason: "stop",
        index: 0,
        message: {
          content: best.content || "No response from any model",
          reasoning: fusedReasoning || undefined,
          role: "assistant",
        },
      },
    ],
    model: "ancf-fusion",
    usage: {
      prompt_tokens: fulfilled.reduce((s, r) => s + (r.value.usage?.prompt_tokens || 0), 0),
      completion_tokens: fulfilled.reduce((s, r) => s + (r.value.usage?.completion_tokens || 0), 0),
      total_tokens: fulfilled.reduce((s, r) => s + (r.value.usage?.total_tokens || 0), 0),
    },
    _fusion: {
      models_used: scored.map((s) => s.model),
      winner: best.model,
      total_models: fulfilled.length,
    },
  };
}
