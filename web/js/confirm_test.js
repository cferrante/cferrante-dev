import { assertEquals } from "./testing.js";
import { initialState, reduce } from "./state.js";
import { confirmBody, secondsLeft } from "./confirm.js";

const site = { siteName: "cferrante.dev", assistantName: "Context Concierge", messages: {} };
const proposal = {
  action_id: "a_1",
  tool: "send_message",
  summary: 'Send the site owner this message: "hello"',
  expires_in_s: 600,
  received_ms: 1_000_000,
};

function proposed() {
  let s = reduce(initialState(site), "ask", { message: "tell him hello" });
  s = reduce(s, "done", { conversation_id: "c_1" });
  return reduce(s, "confirm_request", proposal);
}

Deno.test("confirm_request creates a pending proposal with its expiry", () => {
  assertEquals(proposed().proposal, {
    actionId: "a_1",
    tool: "send_message",
    summary: 'Send the site owner this message: "hello"',
    expiresAtMs: 1_600_000,
    status: "pending",
    outcome: "",
  });
});

Deno.test("Confirm sends the action id in the same conversation", () => {
  assertEquals(confirmBody(proposed()), { conversation_id: "c_1", confirm: { action_id: "a_1" } });
});

Deno.test("the countdown reaches zero at the expiry and stays there", () => {
  const p = proposed().proposal;
  assertEquals(secondsLeft(p, 1_000_000), 600);
  assertEquals(secondsLeft(p, 1_599_500), 1);
  assertEquals(secondsLeft(p, 1_600_000), 0);
  assertEquals(secondsLeft(p, 2_000_000), 0);
});

Deno.test("confirming, then the server's Done, marks the proposal done", () => {
  let s = reduce(proposed(), "confirm", {});
  assertEquals([s.status, s.proposal.status, s.question, s.answer, s.cards], [
    "streaming",
    "confirming",
    'You confirmed: Send the site owner this message: "hello"',
    "",
    [],
  ]);
  s = reduce(s, "token", { text: 'Done: Send the site owner this message: "hello".' });
  s = reduce(s, "done", { conversation_id: "c_1" });
  assertEquals([s.proposal.status, s.proposal.outcome], [
    "done",
    'Done: Send the site owner this message: "hello".',
  ]);
});

Deno.test("each refusal while confirming sets the card's state and a plain message", () => {
  const cases = {
    action_expired: [
      "expired",
      "That request expired before it was confirmed. Ask again if you still want it.",
    ],
    action_already_done: ["done", "That was already done; nothing was sent twice."],
    action_not_found: ["failed", "That request is no longer available. Ask again if you still want it."],
    not_authorized: ["failed", "You can no longer do that. Please sign in again."],
    action_failed: ["failed", "That didn't go through, and it wasn't retried. Please try again later."],
  };
  for (const [code, [status, message]] of Object.entries(cases)) {
    let s = reduce(proposed(), "confirm", {});
    s = reduce(s, "error", { code, message: "technical", retryable: false });
    assertEquals([s.proposal.status, s.proposal.outcome, s.error.retryable], [status, message, false], code);
  }
});

Deno.test("the card stays across new questions until handled; a new proposal replaces it", () => {
  let s = reduce(proposed(), "ask", { message: "another question" });
  assertEquals(s.proposal.actionId, "a_1");
  s = reduce(s, "confirm_request", { ...proposal, action_id: "a_2" });
  assertEquals([s.proposal.actionId, s.proposal.status], ["a_2", "pending"]);
});

Deno.test("Not now dismisses the card without contacting the server", () => {
  assertEquals(reduce(proposed(), "dismiss", {}).proposal, null);
});
