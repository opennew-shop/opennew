/**
 * Benchmark scoring engine
 * Handles 5 evaluation dimensions with automated scoring
 */

// ─── Math scoring (GSM8K) — exact answer extraction ───
export function scoreMath(response, expected) {
  const lastNum = extractLastNumber(response);
  const expectedNum = extractLastNumber(expected);
  if (lastNum === null || expectedNum === null) return 0;
  return Math.abs(lastNum - expectedNum) < 0.01 ? 1 : 0;
}

function extractLastNumber(text) {
  const matches = text.match(/-?\d+(?:\.\d+)?/g);
  return matches ? parseFloat(matches[matches.length - 1]) : null;
}

// ─── Multiple choice scoring (C-Eval) ───
export function scoreChoice(response, expected) {
  const answer = extractChoice(response);
  return answer === expected.toUpperCase() ? 1 : 0;
}

function extractChoice(text) {
  const match = text.match(/\b([A-D])\b/);
  if (match) return match[1];
  // Try 答案：X or 正确答案：X pattern
  const cn = text.match(/答案[是为：:]\s*([A-D])/i);
  if (cn) return cn[1];
  return null;
}

// ─── Code scoring (HumanEval) ───
export function scoreCode(response, testCases) {
  // Extract code block from response
  const code = extractCodeBlock(response);
  if (!code) return 0;

  let passed = 0;
  for (const test of testCases) {
    try {
      const fn = new Function(`"use strict"; return (${code})`)();
      const result = fn(...test.input);
      if (JSON.stringify(result) === JSON.stringify(test.expected)) passed++;
    } catch { /* test failed */ }
  }
  return testCases.length > 0 ? passed / testCases.length : 0;
}

function extractCodeBlock(text) {
  // Try code blocks first
  const block = text.match(/```(?:python|js)?\n([\s\S]*?)```/);
  if (block) return block[1].trim();
  // Fallback: extract function body
  const func = text.match(/(function\s+\w+\s*\([\s\S]*?\})/);
  return func ? func[1] : null;
}

// ─── Translation scoring — LLM-as-Judge ───
export function scoreByJudge(response, question, judgeModel, upstream) {
  const prompt = {
    model: judgeModel,
    messages: [
      { role: "system", content: "You are an evaluation judge. Rate the following response on a scale of 1-10 based on: accuracy, completeness, clarity. Return ONLY a number (1-10)." },
      { role: "user", content: `Question: ${question}\n\nResponse to evaluate: ${response}\n\nScore (1-10):` }
    ],
    max_tokens: 10,
    temperature: 0
  };
  return fetch(`${upstream}/v1/chat/completions`, {
    method: "POST",
    headers: { "Authorization": `Bearer ${process.env.UPSTREAM_API_KEY}`, "Content-Type": "application/json" },
    body: JSON.stringify(prompt)
  }).then(r => r.json()).then(d => {
    const text = d?.choices?.[0]?.message?.content || "0";
    const score = parseInt(text.match(/\d+/)?.[0] || "0");
    return Math.min(10, Math.max(1, score)) / 10;
  }).catch(() => 0.5);
}

// ─── Translation scoring — direct ───
export function scoreTranslation(response, expectedKeywords) {
  if (!response || !expectedKeywords?.length) return 0;
  let hits = 0;
  for (const kw of expectedKeywords) {
    if (response.toLowerCase().includes(kw.toLowerCase())) hits++;
  }
  return hits / expectedKeywords.length;
}

// ─── Overall report ───
export function generateReport(results) {
  const dims = ["math", "choice", "code", "logic", "translation"];
  const models = ["DeepSeek-V4-Flash", "glm-5.1", "kimi-k2.6", "ancf-fusion"];

  console.log("\n╔══════════════════════════════════════════════════════════════╗");
  console.log("║             ANCF Fusion Benchmark Report                     ║");
  console.log("╚══════════════════════════════════════════════════════════════╝\n");

  const scores = {};
  for (const m of models) {
    scores[m] = {};
    let total = 0, count = 0;
    console.log(`─ ${m} ───────────────────────────────`);
    for (const d of dims) {
      const items = results.filter(r => r.model === m && r.dimension === d);
      const avg = items.length > 0 ? items.reduce((s, r) => s + r.score, 0) / items.length : 0;
      scores[m][d] = avg;
      total += avg;
      count++;
      const bar = "█".repeat(Math.round(avg * 20));
      console.log(`  ${d.padEnd(12)} ${(avg * 100).toFixed(1)}% ${bar}`);
    }
    scores[m].avg = count > 0 ? total / count : 0;
    console.log(`  ${"─".repeat(20)}`);
    console.log(`  综合       ${(scores[m].avg * 100).toFixed(1)}%\n`);
  }

  // Fusion comparison
  const singleAvg = (scores["DeepSeek-V4-Flash"].avg + scores["glm-5.1"].avg + scores["kimi-k2.6"].avg) / 3;
  const fusionScore = scores["ancf-fusion"].avg;
  const improvement = fusionScore / singleAvg;

  console.log("═══════════════════════════════════════════════════════════════");
  console.log("  对比总结");
  console.log(`  单模型平均分:  ${(singleAvg * 100).toFixed(1)}%`);
  console.log(`  Fusion 得分:   ${(fusionScore * 100).toFixed(1)}%`);
  console.log(`  提升倍率:      ${improvement.toFixed(2)}x`);
  console.log(`  要求阈值:      ≥ 1.5x（高出50%）`);
  console.log(`  判定:          ${improvement >= 1.5 ? "✅ 有效" : "❌ 未达标"}`);
  console.log("═══════════════════════════════════════════════════════════════\n");

  return { singleAvg, fusionScore, improvement, passed: improvement >= 1.5 };
}
