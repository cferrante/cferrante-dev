// Text for the action cards: titles, durations and detail lines. Every
// number shown comes from the server's measurements (README: never mocked).

import { count, text } from "./messages.js";

// formatDuration keeps about three significant figures.
export function formatDuration(ms) {
  if (ms < 10) return `${Number(ms.toFixed(2))} ms`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${Number((ms / 1000).toFixed(1))} s`;
}

const KIND = {
  retrieval: "Retrieval",
  inference: "Inference",
  tool: "Tool",
  judge: "Judge",
  cache: "Stored answer",
  embed: "Embedding",
  rewrite: "Rewrite",
  rate_limit: "Rate limit",
};

export function cardTitle(card) {
  const kind = KIND[card.kind] || card.kind;
  if (card.kind === "cache") return kind;
  if (card.kind === "inference") {
    const provider = card.detail && card.detail.provider;
    return provider ? `${kind} (${provider})` : kind;
  }
  return card.label ? `${kind} · ${card.label}` : kind;
}

// cardDetails takes the site config for the configurable wording (MESSAGES_FILE).
export function cardDetails(card, config = {}) {
  const d = card.detail;
  if (!d) return [];
  switch (card.kind) {
    case "retrieval":
      return [
        text("retrieval_card", config)
          .replaceAll("{excerpts}", count(d.hits ?? 0, "excerpt", config))
          .replaceAll("{sources}", count(d.sources ?? 0, "source", config))
          .replaceAll("{score}", Number(d.top_score ?? 0).toFixed(3)),
      ];
    case "inference": {
      const speed = d.tokens_per_s ? ` · ${Math.round(d.tokens_per_s)} tok/s` : "";
      const cached = d.cached_tokens ? ` · ${d.cached_tokens} cached` : "";
      return [
        d.model,
        `TTFT ${formatDuration(d.ttft_ms)}${speed}`,
        `${d.tokens_in} in · ${d.tokens_out} out${cached}`,
      ]
        .concat(d.stop_reason && d.stop_reason !== "stop" ? [`stopped: ${d.stop_reason}`] : [])
        .filter(Boolean);
    }
    case "cache":
      return [d.hit ? "pre-verified · no model call" : "not available"];
    case "tool":
      if (d.authorized === false) return ["not authorized"];
      if (d.confirmation === "requested") return ["waiting for your confirmation"];
      return d.error ? [d.error] : [];
  }
  return [];
}
