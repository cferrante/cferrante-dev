// The page: wires the form, the stream and the state to the DOM (design §14).
// Kept thin on purpose: the logic lives in tested modules (state.js,
// client.js, sse.js); this file is checked by the click-through.
//
// Model output is only ever set with textContent, never as HTML, so text a
// prompt injection makes the model write can't become markup or script.
import { postChat } from "./client.js";
import { cardDetails, cardTitle, formatDuration } from "./format.js";
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
  retry: $("retry"),
  cards: $("cards"),
  noCards: $("no-cards"),
  cardTemplate: $("card-template"),
};

// When each active card started, by this page's clock, for the live elapsed
// display. The number shown at the end is always the server's measurement.
const cardStarted = new Map();

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
  els.status.dataset.code = state.error ? state.error.code : "";
  els.retry.hidden = !(state.status === "error" && state.error.retryable && state.question);
  renderCards();
  els.thoughts.scrollTop = els.thoughts.scrollHeight;
}

function renderCards() {
  const nodes = els.cards.children;
  state.cards.forEach((card, i) => {
    let li = nodes[i];
    if (!li) {
      li = els.cardTemplate.content.firstElementChild.cloneNode(true);
      els.cards.append(li);
    }
    li.dataset.state = card.state;
    li.querySelector('[data-role="title"]').textContent = cardTitle(card);
    const done = card.durationMs !== null;
    if (!done && !cardStarted.has(card.id)) cardStarted.set(card.id, performance.now());
    li.querySelector('[data-role="duration"]').textContent = done
      ? formatDuration(card.durationMs)
      : `${formatDuration(performance.now() - cardStarted.get(card.id))}…`;
    const details = li.querySelector('[data-role="details"]');
    const lines = cardDetails(card);
    if (
      details.childElementCount !== lines.length ||
      [...details.children].some((d, j) => d.textContent !== lines[j])
    ) {
      details.replaceChildren(...lines.map((text) => {
        const item = document.createElement("li");
        item.textContent = text;
        return item;
      }));
    }
  });
  while (nodes.length > state.cards.length) nodes[nodes.length - 1].remove();
  els.noCards.hidden = state.cards.length > 0;
}

// Active cards show their elapsed time ticking until the server's figure
// arrives; tick only while something is running.
setInterval(() => {
  if (state.cards.some((c) => c.durationMs === null)) renderCards();
}, 100);

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

async function ask(message) {
  if (!message || state.status === "streaming") return;
  cardStarted.clear();
  apply("ask", { message });
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
      apply("error", { code: "incomplete", message: "the stream ended early" });
    }
  } catch (err) {
    // No HTTP status means the request never got an answer: the server is
    // unreachable ("Failed to fetch"). The raw text goes to the console only.
    apply("error", { code: err.status ? "http" : "network", status: err.status, message: err.message });
  }
  if (state.error) console.warn(`chat error ${state.error.code}`, state.error);
}

els.form.addEventListener("submit", (e) => {
  e.preventDefault();
  const message = els.input.value.trim();
  els.input.value = "";
  ask(message);
});

els.retry.addEventListener("click", () => ask(state.question));

render();
