// The UI state and the pure reducer that applies one stream event to it
// (design §14). The page renders from this state; nothing here touches the DOM.

export function initialState() {
  return {
    status: "idle", // idle | streaming | done | error
    question: "",
    thoughts: [], // [{source: "agent"|"model", text}]
    answer: "",
    conversationId: "",
    error: null, // {code, message, retryable}
  };
}

// reduce returns a new state; the old one is never modified. "ask" is the
// page's own event for a new question.
export function reduce(state, name, data) {
  switch (name) {
    case "ask":
      return { ...state, status: "streaming", question: data.message, thoughts: [], answer: "", error: null };
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
    case "error":
      return {
        ...state,
        status: "error",
        error: { code: data.code, message: data.message, retryable: !!data.retryable },
      };
  }
  return state; // events the page doesn't show yet
}
