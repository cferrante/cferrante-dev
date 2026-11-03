// Plain-language error messages (design §14). The technical code is kept
// for the console and a data attribute; the visitor sees a sentence, never
// raw text such as "Failed to fetch" or a Go error.
//
// The wording is configurable (MESSAGES_FILE, built into dist/config.js by
// orchestrator/cmd/siteconfig) and may use {assistant} and {site}. Whether
// an error offers Retry is not: that's behaviour, decided here.

export const defaultMessages = {
  network: "I can't reach the {assistant} right now. Check your connection, then try again.",
  timeout: "That took too long to answer. Please try again.",
  model_unavailable: "The AI model isn't available right now. Please try again in a moment.",
  retrieval_failed: "I couldn't search the portfolio just now. Please try again.",
  rate_limited: "You've asked a lot in a short time. Please wait a few minutes.",
  tool_limit: "That needed too many steps. Try asking more simply.",
  incomplete: "The answer was cut off. Please try again.",
  session_ended: "Your session has ended. Please sign in again.",
  server_error: "Something went wrong on our side. Please try again.",
  empty_answer: "I didn't manage to put an answer together that time. Please try again.",
  action_expired: "That request expired before it was confirmed. Ask again if you still want it.",
  action_already_done: "That was already done; nothing was sent twice.",
  action_not_found: "That request is no longer available. Ask again if you still want it.",
  not_authorized: "You can no longer do that. Please sign in again.",
  action_failed: "That didn't go through, and it wasn't retried. Please try again later.",
  // The status after the visitor pressed Stop (step 6.0c); not an error.
  stopped: "Stopped. Press Retry for a fresh answer, or ask something else.",
  // Retrieval wording (step 5.3a): visitors read sources and excerpts from the
  // portfolio. The server uses the same keys for the thought stream; a Go test
  // keeps these defaults equal to orchestrator/wording's.
  collection: "portfolio",
  source_one: "source",
  source_other: "sources",
  excerpt_one: "excerpt",
  excerpt_other: "excerpts",
  retrieval_found: "Found {excerpts} from {sources}: {titles}.",
  retrieval_none: "Nothing in the {collection} matched; I'll say so rather than guess.",
  retrieval_card: "{excerpts} · {sources} · top score {score}",
  // The greeting before the first question (step 6.0a); the only wording that
  // may name the owner. The page build stops if a name it uses isn't set.
  welcome:
    "Hi, I'm the {assistant}. Ask about the work in this portfolio, or pick a suggested question below.",
};

const retryable = {
  network: true,
  timeout: true,
  model_unavailable: true,
  retrieval_failed: true,
  rate_limited: false,
  tool_limit: true,
  incomplete: true,
  session_ended: false,
  server_error: true,
  empty_answer: true,
  // Confirmations never offer Retry: re-running an action is the visitor's
  // decision, made by asking again.
  action_expired: false,
  action_already_done: false,
  action_not_found: false,
  not_authorized: false,
  action_failed: false,
};

// text returns a key's wording, configured or default, with {assistant},
// {site}, {owner} and {owner_first} filled in.
export function text(key, config = {}) {
  const template = (config.messages && config.messages[key]) || defaultMessages[key];
  return template
    .replaceAll("{assistant}", config.assistantName || "assistant")
    .replaceAll("{site}", config.siteName || "this site")
    .replaceAll("{owner_first}", config.ownerFirstName || "the site owner")
    .replaceAll("{owner}", config.ownerName || "the site owner");
}

// count renders "1 excerpt" or "4 excerpts" from the noun's _one and _other forms.
export function count(n, noun, config = {}) {
  return `${n} ${text(n === 1 ? `${noun}_one` : `${noun}_other`, config)}`;
}

function message(key, config) {
  return { message: text(key, config), retryable: retryable[key] };
}

// friendlyError takes {code, message, status?} and the site config, and
// returns {message, retryable}.
export function friendlyError(err, config = {}) {
  if (err.code === "http") {
    if (err.status === 401) return message("session_ended", config);
    if (err.status >= 400 && err.status < 500 && err.message) {
      // The handler's 4xx reasons are written for people ("message is empty").
      const m = err.message.trim();
      return { message: m.charAt(0).toUpperCase() + m.slice(1).replace(/\.?$/, "."), retryable: false };
    }
    return message("server_error", config);
  }
  // retryable lists the error codes; defaultMessages also holds other wording.
  return message(err.code in retryable ? err.code : "server_error", config);
}
