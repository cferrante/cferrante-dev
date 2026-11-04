import { assertEquals } from "./testing.js";
import { cardDetails, cardTitle, formatDuration } from "./format.js";

Deno.test("durations read naturally at every scale", () => {
  assertEquals(formatDuration(0.41), "0.41 ms");
  assertEquals(formatDuration(8.2), "8.2 ms");
  assertEquals(formatDuration(543.37), "543 ms");
  assertEquals(formatDuration(6713), "6.7 s");
  assertEquals(formatDuration(84600), "84.6 s");
});

Deno.test("card titles name the step, and the provider for inference", () => {
  assertEquals(cardTitle({ kind: "retrieval", label: "Hybrid search" }), "Retrieval · Hybrid search");
  assertEquals(
    cardTitle({ kind: "inference", label: "Writing the answer", detail: { provider: "groq" } }),
    "Inference (groq)",
  );
  assertEquals(cardTitle({ kind: "inference", label: "Writing the answer" }), "Inference");
  assertEquals(cardTitle({ kind: "tool", label: "send_message" }), "Tool · send_message");
});

Deno.test("detail lines show the measured numbers", () => {
  assertEquals(cardDetails({ kind: "retrieval", detail: { hits: 4, sources: 2, top_score: 0.0327 } }), [
    "4 excerpts · 2 sources · top score 0.033",
  ]);
  assertEquals(cardDetails({ kind: "retrieval", detail: { hits: 1, sources: 1, top_score: 0.5 } }), [
    "1 excerpt · 1 source · top score 0.500",
  ]);
  assertEquals(cardDetails({ kind: "retrieval", detail: { hits: 0, sources: 0 } }), [
    "0 excerpts · 0 sources · top score 0.000",
  ]);
  assertEquals(
    cardDetails({
      kind: "inference",
      detail: {
        model: "qwen3:4b",
        ttft_ms: 6713,
        tokens_in: 237,
        tokens_out: 510,
        tokens_per_s: 16.9,
        cached_tokens: 0,
      },
    }),
    ["qwen3:4b", "TTFT 6.7 s · 17 tok/s", "237 in · 510 out"],
  );
  assertEquals(
    cardDetails({
      kind: "inference",
      detail: {
        model: "gpt-oss-120b",
        ttft_ms: 198,
        tokens_in: 1900,
        tokens_out: 300,
        tokens_per_s: 483,
        cached_tokens: 1856,
      },
    }),
    ["gpt-oss-120b", "TTFT 198 ms · 483 tok/s", "1900 in · 300 out · 1856 cached"],
  );
  assertEquals(cardDetails({ kind: "tool", detail: { tool: "send_message", authorized: false } }), [
    "not authorized",
  ]);
  assertEquals(
    cardDetails({ kind: "tool", detail: { tool: "x", authorized: true, confirmation: "requested" } }),
    ["waiting for your confirmation"],
  );
  assertEquals(cardDetails({ kind: "retrieval" }), []);
});

Deno.test("the inference card says why the model stopped, unless it finished normally", () => {
  const base = { model: "qwen3:4b", ttft_ms: 2700, tokens_in: 470, tokens_out: 220, tokens_per_s: 2.2 };
  assertEquals(
    cardDetails({ kind: "inference", detail: { ...base, stop_reason: "length" } }).at(-1),
    "stopped: length",
  );
  assertEquals(cardDetails({ kind: "inference", detail: { ...base, stop_reason: "stop" } }).length, 3);
  assertEquals(cardDetails({ kind: "inference", detail: base }).length, 3);
});

Deno.test("a stored answer's card says no model was called", () => {
  assertEquals(cardTitle({ kind: "cache", label: "Stored answer" }), "Stored answer");
  assertEquals(cardDetails({ kind: "cache", detail: { hit: true } }), ["pre-verified · no model call"]);
  assertEquals(cardDetails({ kind: "cache", detail: { hit: false } }), ["not available"]);
});

Deno.test("the retrieval card uses the configured wording (MESSAGES_FILE)", () => {
  const config = {
    messages: {
      retrieval_card: "{excerpts} (score {score})",
      excerpt_one: "snippet",
      excerpt_other: "snippets",
    },
  };
  const card = { kind: "retrieval", detail: { hits: 4, sources: 2, top_score: 0.0327 } };
  assertEquals(cardDetails(card, config), ["4 snippets (score 0.033)"]);
  assertEquals(cardDetails({ ...card, detail: { ...card.detail, hits: 1 } }, config), [
    "1 snippet (score 0.033)",
  ]);
});

Deno.test("a card's time: the server's figure, live elapsed, or stopped", async () => {
  const { cardDuration } = await import("./format.js");
  assertEquals(cardDuration({ state: "ok", durationMs: 8.2 }, 99), "8.2 ms");
  assertEquals(cardDuration({ state: "active", durationMs: null }, 1500), "1.5 s…");
  assertEquals(cardDuration({ state: "skipped", durationMs: null }, 1500), "stopped");
});
