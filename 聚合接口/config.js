export const UPSTREAM = "https://aigw-gzgy2.cucloud.cn:8443";
export const UPSTREAM_KEY = process.env.UPSTREAM_API_KEY || "";
export const GATEWAY_API_KEY = process.env.GATEWAY_API_KEY || "";
export const MODELS = {
  "ancf-fusion": { description: "ANCF Fusion - parallel aggregation of DeepSeek-V4-Flash, glm-5.1, kimi-k2.6", sub_models: ["DeepSeek-V4-Flash", "glm-5.1", "kimi-k2.6"], capabilities: { vision: true, reasoning: true } },
  "DeepSeek-V4-Flash": { description: "DeepSeek V4 Flash - fast reasoning", upstream_model: "DeepSeek-V4-Flash", capabilities: { vision: false, reasoning: true } },
  "glm-5.1": { description: "GLM 5.1 - general purpose", upstream_model: "glm-5.1", capabilities: { vision: false, reasoning: true } },
  "kimi-k2.6": { description: "Kimi K2.6 - multimodal vision + reasoning", upstream_model: "kimi-k2.6", capabilities: { vision: true, reasoning: true } }
};
export const HAS_VISION = ["kimi-k2.6"];
export const VISION_MODELS = ["kimi-k2.6"];
