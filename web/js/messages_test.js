import { assertEquals } from "./testing.js";
import { friendlyError } from "./messages.js";

const site = { siteName: "cferrante.dev", assistantName: "Context Concierge", messages: {} };

Deno.test("an unreachable server gets a plain sentence naming the assistant, retryable", () => {
  assertEquals(friendlyError({ code: "network", message: "Failed to fetch" }, site), {
    message: "I can't reach the Context Concierge right now. Check your connection, then try again.",
    retryable: true,
  });
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
    assertEquals(friendlyError({ code, message: "technical text" }, site), { message, retryable }, code);
  }
});

Deno.test("HTTP errors: 401 asks to sign in, 5xx is our fault, 400 keeps the server's reason", () => {
  assertEquals(friendlyError({ code: "http", status: 401, message: "not signed in" }, site), {
    message: "Your session has ended. Please sign in again.",
    retryable: false,
  });
  assertEquals(friendlyError({ code: "http", status: 502, message: "bad gateway" }, site), {
    message: "Something went wrong on our side. Please try again.",
    retryable: true,
  });
  assertEquals(
    friendlyError({ code: "http", status: 400, message: "message is longer than 2000 characters" }, site),
    {
      message: "Message is longer than 2000 characters.",
      retryable: false,
    },
  );
});

Deno.test("anything unknown falls back to a generic sentence, never the raw text", () => {
  assertEquals(friendlyError({ code: "weird_new_code", message: "panic: nil map" }, site), {
    message: "Something went wrong on our side. Please try again.",
    retryable: true,
  });
});

Deno.test("configured wording replaces the default and fills placeholders", () => {
  const custom = {
    siteName: "example.org",
    assistantName: "Concierge",
    messages: { network: "{assistant} at {site} is offline.", server_error: "Oops at {site}." },
  };
  assertEquals(friendlyError({ code: "network" }, custom), {
    message: "Concierge at example.org is offline.",
    retryable: true,
  });
  assertEquals(friendlyError({ code: "mystery" }, custom), {
    message: "Oops at example.org.",
    retryable: true,
  });
  // Not overridden: the default, with the configured name.
  assertEquals(
    friendlyError({ code: "timeout" }, custom).message,
    "That took too long to answer. Please try again.",
  );
});

Deno.test("wording is configurable; whether Retry is offered is not", () => {
  const custom = { ...site, messages: { rate_limited: "Slow down, please.", timeout: "Too slow." } };
  assertEquals(friendlyError({ code: "rate_limited" }, custom), {
    message: "Slow down, please.",
    retryable: false,
  });
  assertEquals(friendlyError({ code: "timeout" }, custom), { message: "Too slow.", retryable: true });
});

Deno.test("works with no config at all", () => {
  assertEquals(
    friendlyError({ code: "network" }).message,
    "I can't reach the assistant right now. Check your connection, then try again.",
  );
});

Deno.test("a model that stops without an answer gets its own sentence, retryable", () => {
  assertEquals(
    friendlyError({ code: "empty_answer", message: "the model finished without an answer" }, site),
    {
      message: "I didn't manage to put an answer together that time. Please try again.",
      retryable: true,
    },
  );
});
