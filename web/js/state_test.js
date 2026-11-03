import { assertEquals } from "./testing.js";
import { initialState, reduce } from "./state.js";

function run(events) {
  let s = initialState();
  for (const [name, data] of events) s = reduce(s, name, data);
  return s;
}

Deno.test("starts idle and empty", () => {
  const s = initialState();
  assertEquals([s.status, s.thoughts, s.answer, s.cards], ["idle", [], "", []]);
});

Deno.test("model reasoning chunks join into one block; agent lines stay separate", () => {
  const s = run([
    ["thought", { text: "Found 2 excerpts from 1 source: Project Alpha (2).", source: "agent" }],
    ["thought", { text: "The user ", source: "model" }],
    ["thought", { text: "asks about Go.", source: "model" }],
    ["thought", { text: "Calling a tool.", source: "agent" }],
    ["thought", { text: "Now answer.", source: "model" }],
  ]);
  assertEquals(s.thoughts, [
    { source: "agent", text: "Found 2 excerpts from 1 source: Project Alpha (2)." },
    { source: "model", text: "The user asks about Go." },
    { source: "agent", text: "Calling a tool." },
    { source: "model", text: "Now answer." },
  ]);
});

Deno.test("tokens build the answer; done records the conversation", () => {
  const s = run([
    ["token", { text: "Alpha is " }],
    ["token", { text: "Go [1]." }],
    ["done", { conversation_id: "c_1", total_ms: 37000 }],
  ]);
  assertEquals([s.answer, s.status, s.conversationId], ["Alpha is Go [1].", "done", "c_1"]);
});

Deno.test("an error ends the turn and keeps what arrived", () => {
  const s = run([
    ["token", { text: "half" }],
    ["error", { code: "timeout", message: "the answer took too long", retryable: true }],
  ]);
  // The wording is messages.js's job (tested there and below); here: the
  // turn ends, keeps its partial answer, and remembers the code.
  assertEquals([s.status, s.answer, s.error.code], ["error", "half", "timeout"]);
});

Deno.test("a new question clears the last answer but keeps the conversation", () => {
  let s = run([["token", { text: "old" }], ["done", { conversation_id: "c_1" }]]);
  s = reduce(s, "ask", { message: "next?" });
  assertEquals([s.status, s.answer, s.thoughts, s.question, s.conversationId], [
    "streaming",
    "",
    [],
    "next?",
    "c_1",
  ]);
});

Deno.test("the reducer never mutates the previous state, for any event", () => {
  const before = run([["thought", { text: "a", source: "model" }], ["token", { text: "x" }]]);
  const snapshot = JSON.stringify(before);
  for (
    const [name, data] of [
      ["thought", { text: "b", source: "model" }],
      ["thought", { text: "c", source: "agent" }],
      ["token", { text: "y" }],
      ["done", { conversation_id: "c_2" }],
      ["error", { code: "x", message: "y" }],
      ["ask", { message: "q" }],
    ]
  ) {
    reduce(before, name, data);
    assertEquals(JSON.stringify(before), snapshot, `after ${name}`);
  }
});

Deno.test("unknown events are ignored", () => {
  const s = initialState();
  assertEquals(reduce(s, "future_event", { x: 1 }), s);
});

Deno.test("step_start creates an active card; step_end fills it in place", () => {
  const s = run([
    ["step_start", { step_id: "retrieval", kind: "retrieval", label: "Hybrid search", t_ms: 0.1 }],
    ["step_start", { step_id: "inference", kind: "inference", label: "Writing the answer", t_ms: 103 }],
    ["step_end", { step_id: "retrieval", status: "ok", duration_ms: 102.7, detail: { hits: 4 } }],
  ]);
  assertEquals(s.cards, [
    {
      id: "retrieval",
      kind: "retrieval",
      label: "Hybrid search",
      state: "ok",
      durationMs: 102.7,
      detail: { hits: 4 },
    },
    {
      id: "inference",
      kind: "inference",
      label: "Writing the answer",
      state: "active",
      durationMs: null,
      detail: null,
    },
  ]);
});

Deno.test("a fallback or error status becomes the card's state", () => {
  const s = run([
    ["step_start", { step_id: "a", kind: "inference", label: "x" }],
    ["step_end", { step_id: "a", status: "fallback", duration_ms: 5, detail: { provider: "bedrock" } }],
    ["step_start", { step_id: "b", kind: "tool", label: "y" }],
    ["step_end", { step_id: "b", status: "error", duration_ms: 1, detail: { authorized: false } }],
  ]);
  assertEquals(s.cards.map((c) => c.state), ["fallback", "error"]);
});

Deno.test("a new question clears the cards", () => {
  let s = run([["step_start", { step_id: "a", kind: "retrieval", label: "x" }]]);
  s = reduce(s, "ask", { message: "next" });
  assertEquals(s.cards, []);
});

Deno.test("errors are made friendly, and the question is kept for Retry", () => {
  const config = { siteName: "cferrante.dev", assistantName: "Context Concierge", messages: {} };
  let s = reduce(initialState(config), "ask", { message: "How does Alpha search?" });
  s = reduce(s, "error", { code: "timeout", message: "the answer took too long", retryable: true });
  assertEquals(s.error, {
    code: "timeout",
    message: "That took too long to answer. Please try again.",
    retryable: true,
  });
  assertEquals(s.question, "How does Alpha search?");
  s = reduce(s, "error", { code: "network", message: "Failed to fetch" });
  assertEquals(
    s.error.message,
    "I can't reach the Context Concierge right now. Check your connection, then try again.",
  );
});

Deno.test("the site config travels in the state and shapes error wording", () => {
  const config = {
    siteName: "x.org",
    assistantName: "Concierge",
    messages: { timeout: "{assistant} gave up." },
  };
  let s = initialState(config);
  assertEquals(s.config, config);
  s = reduce(reduce(s, "ask", { message: "q" }), "error", { code: "timeout" });
  assertEquals(s.error.message, "Concierge gave up.");
});

Deno.test("citations are collected once each and cleared by a new question", () => {
  let s = run([
    ["citation", { n: 2, chunk_id: "b", title: "B", url: "/b" }],
    ["citation", { n: 1, chunk_id: "a", title: "A", url: "/a" }],
    ["citation", { n: 2, chunk_id: "b", title: "B", url: "/b" }],
  ]);
  assertEquals(s.citations.map((c) => c.n), [2, 1]);
  s = reduce(s, "ask", { message: "next" });
  assertEquals(s.citations, []);
});
