import { assertEquals } from "./testing.js";
import { friendlyError } from "./messages.js";

Deno.test("an unreachable server gets a plain sentence, retryable", () => {
  const e = friendlyError({ code: "network", message: "Failed to fetch" });
  assertEquals(e.retryable, true);
  assertEquals(e.message, "I can't reach the receptionist right now. Check your connection, then try again.");
});

Deno.test("each stream error code has its own sentence", () => {
  const cases = {
    timeout: ["That took too long to answer. Please try again.", true],
    model_unavailable: ["The AI model isn't available right now. Please try again in a moment.", true],
    retrieval_failed: ["I couldn't search the portfolio just now. Please try again.", true],
    rate_limited: ["You've asked a lot in a short time. Please wait a few minutes.", false],
    tool_limit: ["That needed too many steps. Try asking more simply.", true],
    incomplete: ["The answer was cut off. Please try again.", true],
  };
  for (const [code, [message, retryable]] of Object.entries(cases)) {
    assertEquals(friendlyError({ code, message: "technical text" }), { message, retryable }, code);
  }
});

Deno.test("HTTP errors: 401 asks to sign in, 5xx is our fault, 400 keeps the server's reason", () => {
  assertEquals(friendlyError({ code: "http", status: 401, message: "not signed in" }), {
    message: "Your session has ended. Please sign in again.",
    retryable: false,
  });
  assertEquals(friendlyError({ code: "http", status: 502, message: "bad gateway" }), {
    message: "Something went wrong on our side. Please try again.",
    retryable: true,
  });
  assertEquals(
    friendlyError({ code: "http", status: 400, message: "message is longer than 2000 characters" }),
    { message: "Message is longer than 2000 characters.", retryable: false },
  );
});

Deno.test("anything unknown falls back to a generic sentence, never the raw text", () => {
  const e = friendlyError({ code: "weird_new_code", message: "panic: nil map" });
  assertEquals(e, { message: "Something went wrong on our side. Please try again.", retryable: true });
});
