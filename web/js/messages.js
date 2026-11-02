// Plain-language error messages (design §14). The technical code stays on
// the error for the console and a data attribute; the visitor sees a
// sentence, never raw text such as "Failed to fetch" or a Go error.

const GENERIC = { message: "Something went wrong on our side. Please try again.", retryable: true };

const BY_CODE = {
  network: ["I can't reach the receptionist right now. Check your connection, then try again.", true],
  timeout: ["That took too long to answer. Please try again.", true],
  model_unavailable: ["The AI model isn't available right now. Please try again in a moment.", true],
  retrieval_failed: ["I couldn't search the portfolio just now. Please try again.", true],
  rate_limited: ["You've asked a lot in a short time. Please wait a few minutes.", false],
  tool_limit: ["That needed too many steps. Try asking more simply.", true],
  incomplete: ["The answer was cut off. Please try again.", true],
};

// friendlyError takes {code, message, status?} and returns {message, retryable}.
export function friendlyError(err) {
  if (err.code === "http") {
    if (err.status === 401) {
      return { message: "Your session has ended. Please sign in again.", retryable: false };
    }
    if (err.status >= 400 && err.status < 500 && err.message) {
      // The handler's 4xx reasons are written for people ("message is empty").
      const m = err.message.trim();
      return { message: m.charAt(0).toUpperCase() + m.slice(1).replace(/\.?$/, "."), retryable: false };
    }
    return GENERIC;
  }
  const known = BY_CODE[err.code];
  return known ? { message: known[0], retryable: known[1] } : GENERIC;
}
