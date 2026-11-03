// The page: wires the form, the stream and the state to the DOM (design §14).
// Kept thin on purpose: the logic lives in tested modules (state.js,
// client.js, sse.js); this file is checked by the click-through.
//
// Model output is only ever set with textContent, never as HTML, so text a
// prompt injection makes the model write can't become markup or script.
import { getSuggested, postChat } from "./client.js";
import { linkify, sources } from "./cite.js";
import { confirmBody, secondsLeft } from "./confirm.js";
import { cardDetails, cardTitle, formatDuration } from "./format.js";
import { initialState, reduce, shownAnswer } from "./state.js";
// Built by `make site` (orchestrator/cmd/siteconfig) from SITE_NAME,
// ASSISTANT_NAME, OWNER_NAME, OWNER_FIRST_NAME and MESSAGES_FILE; shown as
// text only.
import site from "../dist/config.js";

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
  proposals: $("proposals"),
  chips: $("chips"),
  sources: $("sources"),
  sourcesBlock: $("sources-block"),
  proposalTemplate: $("proposal-template"),
};

const proposalTitle = {
  pending: "Please confirm",
  confirming: "Sending…",
  done: "Done",
  expired: "Expired",
  failed: "Not sent",
};

// When each active card started, by this page's clock, for the live elapsed
// display. The number shown at the end is always the server's measurement.
const cardStarted = new Map();

// Complete class names in lookup maps, so Tailwind's scanner sees them.
const thoughtClass = {
  agent: "whitespace-pre-wrap text-term-accent",
  model: "whitespace-pre-wrap text-term-dim",
};

$("site-name").textContent = site.siteName;
$("assistant-name").textContent = site.assistantName;
document.title = `${site.siteName} ${site.assistantName}`;

let state = initialState(site);
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
  renderAnswer();
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
  renderProposals();
  els.thoughts.scrollTop = els.thoughts.scrollHeight;
}

// The answer (or the welcome, before the first question) with its citation
// links: built from text nodes and <a> elements (href only from cite.js's
// safe-URL check), never from HTML. Rebuilt only when the text or its
// citations change.
let renderedAnswer = null;
function renderAnswer() {
  const shown = shownAnswer(state);
  const key = shown + "\u0000" + state.citations.length;
  if (key === renderedAnswer) return;
  renderedAnswer = key;
  els.answer.replaceChildren(
    ...linkify(shown, state.citations).map((seg) => {
      if (seg.href === undefined) return document.createTextNode(seg.text);
      return link(seg.text, seg.href, seg.title);
    }),
  );
  const list = sources(state.citations);
  els.sourcesBlock.hidden = list.length === 0;
  els.sources.replaceChildren(...list.map((src) => {
    const li = document.createElement("li");
    li.append(`[${src.n}] `, src.href ? link(src.title, src.href, src.title) : src.title);
    return li;
  }));
}

function link(text, href, title) {
  const a = document.createElement("a");
  a.textContent = text;
  a.href = href;
  a.title = title || "";
  a.target = "_blank"; // keep the conversation open
  a.rel = "noopener noreferrer";
  a.className = "text-term-accent underline hover:no-underline";
  return a;
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
    const lines = cardDetails(card, site);
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

function renderProposals() {
  const nodes = els.proposals.children;
  const now = Date.now();
  state.proposals.forEach((p, i) => {
    let li = nodes[i];
    if (!li || li.dataset.actionId !== p.actionId) {
      li = els.proposalTemplate.content.firstElementChild.cloneNode(true);
      li.dataset.actionId = p.actionId;
      if (nodes[i]) nodes[i].replaceWith(li);
      else els.proposals.append(li);
    }
    const left = secondsLeft(p, now);
    const status = p.status === "pending" && left === 0 ? "expired" : p.status;
    li.dataset.status = status;
    const part = (role) => li.querySelector(`[data-role="${role}"]`);
    part("title").textContent = proposalTitle[status];
    part("summary").textContent = p.summary;
    part("outcome").textContent = status === "expired" && !p.outcome
      ? "Nothing was sent. Ask again if you still want it."
      : p.outcome;
    const waiting = status === "pending";
    part("confirm").hidden = part("dismiss").hidden = !waiting;
    part("confirm").disabled = state.status === "streaming";
    part("time").textContent = waiting
      ? `expires in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`
      : "";
  });
  while (nodes.length > state.proposals.length) nodes[nodes.length - 1].remove();
}

// Active cards show their elapsed time ticking until the server's figure
// arrives; tick only while something is running.
setInterval(() => {
  if (state.cards.some((c) => c.durationMs === null)) renderCards();
  if (state.proposals.some((p) => p.status === "pending")) renderProposals();
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

// send posts one request and streams its events into the state. start is
// the page's own event for it: "ask" for a question, "confirm" for Confirm.
// lastRequest is what Retry repeats: a question, or a confirmation (safe to
// resend, since a pending action runs at most once).
let lastRequest = null;

async function send(start, startData, body) {
  if (state.status === "streaming") return;
  lastRequest = [start, startData, body];
  cardStarted.clear();
  apply(start, startData);
  try {
    await postChat(body(), (name, data) => {
      // The reducer is pure, so the page supplies the arrival time the
      // Confirm card's countdown is measured from.
      apply(name, name === "confirm_request" ? { ...data, received_ms: Date.now() } : data);
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

function ask(message) {
  if (!message) return;
  send("ask", { message, now_ms: Date.now() }, () => ({ conversation_id: state.conversationId, message }));
}

els.form.addEventListener("submit", (e) => {
  e.preventDefault();
  const message = els.input.value.trim();
  els.input.value = "";
  ask(message);
});

els.retry.addEventListener("click", () => lastRequest && send(...lastRequest));
// Suggestion chips: each sends its id, and the server streams the stored
// answer with no model call.
getSuggested().then((list) => {
  els.chips.replaceChildren(...list.map((s) => {
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = s.question;
    b.className =
      "rounded-full border border-term-line px-3 py-1 text-xs text-term-fg hover:border-term-accent focus-visible:outline-2 focus-visible:outline-term-accent";
    b.addEventListener("click", () =>
      send("ask", { message: s.question, now_ms: Date.now() }, () => ({
        conversation_id: state.conversationId,
        suggested_id: s.id,
      })));
    return b;
  }));
});

// One listener for every Confirm card's buttons.
els.proposals.addEventListener("click", (e) => {
  const button = e.target.closest("button[data-role]");
  const actionId = button && button.closest("li").dataset.actionId;
  if (!actionId) return;
  if (button.dataset.role === "confirm") send("confirm", { actionId }, () => confirmBody(state, actionId));
  if (button.dataset.role === "dismiss") apply("dismiss", { actionId });
});

render();
