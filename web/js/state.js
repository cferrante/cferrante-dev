// The UI state and the pure reducer that applies one stream event to it
// (design §14). The page renders from this state; nothing here touches the DOM.
import { friendlyError } from "./messages.js";

export function initialState(config = {}) {
  return {
    config, // site settings from dist/config.js: names and message wording
    status: "idle", // idle | streaming | done | error
    question: "",
    thoughts: [], // [{source: "agent"|"model", text}]
    answer: "",
    conversationId: "",
    error: null, // {code, message, retryable}: message is the friendly one
    cards: [], // action cards: [{id, kind, label, state, durationMs, detail}]
    // Confirm cards, one per proposed action:
    // [{actionId, tool, summary, expiresAtMs, status, outcome}], where status is
    // pending | confirming | done | expired | failed. A card still waiting stays
    // across new questions; handled and expired cards are cleared by the next one.
    proposals: [],
  };
}

const updateProposals = (state, pred, change) =>
  state.proposals.map((p) => pred(p) ? { ...p, ...change } : p);

// reduce returns a new state; the old one is never modified. "ask", "confirm"
// and "dismiss" are the page's own events.
export function reduce(state, name, data) {
  switch (name) {
    case "ask":
      return {
        ...state,
        status: "streaming",
        question: data.message,
        thoughts: [],
        answer: "",
        error: null,
        cards: [],
        // Keep only cards still waiting for the visitor (now_ms is when the
        // question was asked, since the reducer can't read the clock).
        proposals: state.proposals.filter((p) =>
          p.status === "pending" && (data.now_ms === undefined || p.expiresAtMs > data.now_ms)
        ),
      };
    case "thought": {
      const last = state.thoughts[state.thoughts.length - 1];
      // Reasoning streams a few characters at a time: join it into one
      // block. Each agent narration is its own line.
      if (data.source === "model" && last && last.source === "model") {
        return {
          ...state,
          thoughts: [...state.thoughts.slice(0, -1), { source: "model", text: last.text + data.text }],
        };
      }
      return { ...state, thoughts: [...state.thoughts, { source: data.source, text: data.text }] };
    }
    case "token":
      return { ...state, answer: state.answer + data.text };
    case "done":
      return {
        ...state,
        status: "done",
        conversationId: data.conversation_id || state.conversationId,
        proposals: updateProposals(state, (p) => p.status === "confirming", {
          status: "done",
          outcome: state.answer,
        }),
      };
    case "confirm_request":
      if (state.proposals.some((p) => p.actionId === data.action_id)) return state;
      return {
        ...state,
        proposals: [...state.proposals, {
          actionId: data.action_id,
          tool: data.tool,
          summary: data.summary,
          expiresAtMs: data.received_ms + data.expires_in_s * 1000,
          status: "pending",
          outcome: "",
        }],
      };
    case "confirm": { // the visitor pressed Confirm on data.actionId
      const p = state.proposals.find((x) => x.actionId === data.actionId);
      if (!p) return state;
      return {
        ...state,
        status: "streaming",
        question: `You confirmed: ${p.summary}`,
        thoughts: [],
        answer: "",
        error: null,
        cards: [],
        proposals: updateProposals(state, (x) => x.actionId === data.actionId, { status: "confirming" }),
      };
    }
    case "dismiss": // Not now: nothing is sent; the action simply expires
      return { ...state, proposals: state.proposals.filter((p) => p.actionId !== data.actionId) };
    case "step_start":
      return {
        ...state,
        cards: [...state.cards, {
          id: data.step_id,
          kind: data.kind,
          label: data.label,
          state: "active",
          durationMs: null,
          detail: null,
        }],
      };
    case "step_end":
      // state is ok | error | fallback | skipped, used as data-state for styling.
      return {
        ...state,
        cards: state.cards.map((c) =>
          c.id === data.step_id
            ? { ...c, state: data.status, durationMs: data.duration_ms, detail: data.detail || null }
            : c
        ),
      };
    case "error": {
      const error = { code: data.code, ...friendlyError(data, state.config) };
      const status = { action_expired: "expired", action_already_done: "done" }[data.code] || "failed";
      return {
        ...state,
        status: "error",
        error,
        proposals: updateProposals(state, (p) => p.status === "confirming", {
          status,
          outcome: error.message,
        }),
      };
    }
  }
  return state; // events the page doesn't show yet
}
