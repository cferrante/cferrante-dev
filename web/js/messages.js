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
};

function wording(key, config) {
  const template = (config.messages && config.messages[key]) || defaultMessages[key];
  return template
    .replaceAll("{assistant}", config.assistantName || "assistant")
    .replaceAll("{site}", config.siteName || "this site");
}

function message(key, config) {
  return { message: wording(key, config), retryable: retryable[key] };
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
  return message(err.code in defaultMessages ? err.code : "server_error", config);
}
