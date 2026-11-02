// The Confirm card's pure helpers (design §6, §14).

// confirmBody is the POST /chat body that confirms the pending proposal, in
// the conversation it was proposed in.
export function confirmBody(state) {
  return { conversation_id: state.conversationId, confirm: { action_id: state.proposal.actionId } };
}

// secondsLeft is how long the proposal can still be confirmed, at nowMs,
// rounded up so "1 s" shows until it really expires.
export function secondsLeft(proposal, nowMs) {
  return Math.max(0, Math.ceil((proposal.expiresAtMs - nowMs) / 1000));
}
