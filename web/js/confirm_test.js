import { assertEquals } from "./testing.js";
import { initialState, reduce } from "./state.js";
import { confirmBody, secondsLeft } from "./confirm.js";

const site = { siteName: "cferrante.dev", assistantName: "Context Concierge", messages: {} };
const req = (id, text, received_ms = 1_000_000) => ({
  action_id: id,
  tool: "send_message",
  summary: `Send the site owner this message: "${text}"`,
  expires_in_s: 600,
  received_ms,
});

function proposed() {
  let s = reduce(initialState(site), "ask", { message: "tell him hello", now_ms: 1_000_000 });
  s = reduce(s, "confirm_request", req("a_1", "hello"));
  return reduce(s, "done", { conversation_id: "c_1" });
}

Deno.test("confirm_request adds a pending proposal with its expiry", () => {
  assertEquals(proposed().proposals, [{
    actionId: "a_1",
    tool: "send_message",
    summary: 'Send the site owner this message: "hello"',
    expiresAtMs: 1_600_000,
    status: "pending",
    outcome: "",
  }]);
});

Deno.test("Confirm sends that action's id in the same conversation", () => {
  assertEquals(confirmBody(proposed(), "a_1"), { conversation_id: "c_1", confirm: { action_id: "a_1" } });
});

Deno.test("the countdown reaches zero at the expiry and stays there", () => {
  const p = proposed().proposals[0];
  assertEquals(secondsLeft(p, 1_000_000), 600);
  assertEquals(secondsLeft(p, 1_599_500), 1);
  assertEquals(secondsLeft(p, 1_600_000), 0);
  assertEquals(secondsLeft(p, 2_000_000), 0);
});

Deno.test("confirming, then the server's Done, marks that proposal done", () => {
  let s = reduce(proposed(), "confirm", { actionId: "a_1" });
  assertEquals([s.status, s.proposals[0].status, s.question, s.answer, s.cards], [
    "streaming",
    "confirming",
    'You confirmed: Send the site owner this message: "hello"',
    "",
    [],
  ]);
  s = reduce(s, "token", { text: 'Done: Send the site owner this message: "hello".' });
  s = reduce(s, "done", { conversation_id: "c_1" });
  assertEquals([s.proposals[0].status, s.proposals[0].outcome], [
    "done",
    'Done: Send the site owner this message: "hello".',
  ]);
});

Deno.test("each refusal while confirming sets that card's state and a plain message", () => {
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
    let s = reduce(proposed(), "confirm", { actionId: "a_1" });
    s = reduce(s, "error", { code, message: "technical", retryable: false });
    assertEquals(
      [s.proposals[0].status, s.proposals[0].outcome, s.error.retryable],
      [status, message, false],
      code,
    );
  }
});

// Your click-through, step 6: a second proposal replaced the first, which
// was still waiting. Each pending action now keeps its own card.
Deno.test("a second proposal adds a card; it never replaces one still waiting", () => {
  let s = reduce(proposed(), "ask", { message: "and tell him goodbye", now_ms: 1_100_000 });
  s = reduce(s, "confirm_request", req("a_2", "goodbye", 1_100_000));
  assertEquals(s.proposals.map((p) => [p.actionId, p.status]), [["a_1", "pending"], ["a_2", "pending"]]);
  // Confirming one leaves the other waiting.
  s = reduce(s, "confirm", { actionId: "a_2" });
  s = reduce(reduce(s, "token", { text: "Done." }), "done", {});
  assertEquals(s.proposals.map((p) => [p.actionId, p.status]), [["a_1", "pending"], ["a_2", "done"]]);
  // The same id arriving twice doesn't duplicate the card.
  assertEquals(reduce(s, "confirm_request", req("a_1", "hello")).proposals.length, 2);
});

// Your click-through, step 5: a Done card stayed up while the next answer
// was being written. A new question clears handled and expired cards.
Deno.test("a new question clears finished and expired cards, keeping those still waiting", () => {
  let s = proposed(); // a_1 expires at 1_600_000
  s = reduce(s, "confirm_request", req("a_2", "two", 1_300_000)); // expires 1_900_000
  s = reduce(s, "confirm_request", req("a_3", "three", 1_300_000));
  s = reduce(s, "confirm", { actionId: "a_3" });
  s = reduce(reduce(s, "token", { text: "Done." }), "done", {});
  s = reduce(s, "ask", { message: "next", now_ms: 1_700_000 }); // a_1 expired, a_3 done
  assertEquals(s.proposals.map((p) => p.actionId), ["a_2"]);
});

Deno.test("Not now dismisses only that card, without contacting the server", () => {
  let s = reduce(proposed(), "confirm_request", req("a_2", "two"));
  s = reduce(s, "dismiss", { actionId: "a_1" });
  assertEquals(s.proposals.map((p) => p.actionId), ["a_2"]);
});
