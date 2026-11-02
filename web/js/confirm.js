// The Confirm cards' pure helpers (design §6, §14).

// confirmBody is the POST /chat body that confirms one proposal, in the
// conversation it was proposed in.
export function confirmBody(state, actionId) {
  return { conversation_id: state.conversationId, confirm: { action_id: actionId } };
}

// secondsLeft is how long a proposal can still be confirmed, at nowMs,
// rounded up so "1 s" shows until it really expires.
export function secondsLeft(proposal, nowMs) {
  return Math.max(0, Math.ceil((proposal.expiresAtMs - nowMs) / 1000));
}
