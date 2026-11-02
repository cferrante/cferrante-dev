import { assertEquals } from "./testing.js";
import { initialState, reduce } from "./state.js";

function run(events) {
  let s = initialState();
  for (const [name, data] of events) s = reduce(s, name, data);
  return s;
}

Deno.test("starts idle and empty", () => {
  const s = initialState();
  assertEquals([s.status, s.thoughts, s.answer], ["idle", [], ""]);
});

Deno.test("model reasoning chunks join into one block; agent lines stay separate", () => {
  const s = run([
    ["thought", { text: "Found 2 passages.", source: "agent" }],
    ["thought", { text: "The user ", source: "model" }],
    ["thought", { text: "asks about Go.", source: "model" }],
    ["thought", { text: "Calling a tool.", source: "agent" }],
    ["thought", { text: "Now answer.", source: "model" }],
  ]);
  assertEquals(s.thoughts, [
    { source: "agent", text: "Found 2 passages." },
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
  assertEquals([s.status, s.answer, s.error], ["error", "half", {
    code: "timeout",
    message: "the answer took too long",
    retryable: true,
  }]);
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
