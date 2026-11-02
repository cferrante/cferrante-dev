// The page: wires the form, the stream and the state to the DOM (design §14).
// Kept thin on purpose: the logic lives in tested modules (state.js,
// client.js, sse.js); this file is checked by the click-through.
//
// Model output is only ever set with textContent, never as HTML, so text a
// prompt injection makes the model write can't become markup or script.
import { postChat } from "./client.js";
import { initialState, reduce } from "./state.js";

const $ = (id) => document.getElementById(id);
const els = {
  form: $("ask"),
  input: $("question"),
  send: $("send"),
  thoughts: $("thoughts"),
  asked: $("asked"),
  answer: $("answer"),
  status: $("status"),
};

// Complete class names in lookup maps, so Tailwind's scanner sees them.
const thoughtClass = {
  agent: "whitespace-pre-wrap text-term-accent",
  model: "whitespace-pre-wrap text-term-dim",
};

let state = initialState();
try {
  state = { ...state, conversationId: sessionStorage.getItem("conversation_id") || "" };
} catch { /* storage unavailable: start a new conversation */ }

function render() {
  const nodes = els.thoughts.children;
  state.thoughts.forEach((t, i) => {
    let el = nodes[i];
    if (!el) {
      el = document.createElement("p");
      el.className = thoughtClass[t.source] || thoughtClass.model;
      els.thoughts.append(el);
    }
    const text = t.source === "agent" ? `> ${t.text}` : t.text;
    if (el.textContent !== text) el.textContent = text;
  });
  while (nodes.length > state.thoughts.length) nodes[nodes.length - 1].remove();

  els.asked.textContent = state.question ? `You: ${state.question}` : "";
  els.answer.textContent = state.answer;
  els.status.dataset.state = state.status;
  els.status.textContent = state.status === "streaming"
    ? "Thinking…"
    : state.status === "error"
    ? `Error: ${state.error.message}`
    : "";
  els.send.disabled = state.status === "streaming";
  els.thoughts.scrollTop = els.thoughts.scrollHeight;
}

// Hundreds of reasoning events arrive per answer; repaint at most once per
// frame rather than once per event.
let framePending = false;
function apply(name, data) {
  state = reduce(state, name, data);
  if (!framePending) {
    framePending = true;
    requestAnimationFrame(() => {
      framePending = false;
      render();
    });
  }
}

els.form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const message = els.input.value.trim();
  if (!message || state.status === "streaming") return;
  apply("ask", { message });
  els.input.value = "";
  try {
    await postChat({ conversation_id: state.conversationId, message }, (name, data) => {
      apply(name, data);
      if (name === "done") {
        try {
          sessionStorage.setItem("conversation_id", data.conversation_id);
        } catch { /* storage unavailable: the next question starts afresh */ }
      }
    });
    if (state.status === "streaming") {
      apply("error", { code: "incomplete", message: "the stream ended early", retryable: true });
    }
  } catch (err) {
    apply("error", { code: "http", message: err.message, retryable: true });
  }
});

render();
