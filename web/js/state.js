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
  };
}

// reduce returns a new state; the old one is never modified. "ask" is the
// page's own event for a new question.
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
      return { ...state, status: "done", conversationId: data.conversation_id || state.conversationId };
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
    case "error":
      return { ...state, status: "error", error: { code: data.code, ...friendlyError(data, state.config) } };
  }
  return state; // events the page doesn't show yet
}
