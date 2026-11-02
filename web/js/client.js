// POST /chat and stream the reply (design §3, §14). fetchFn is injectable so
// tests run without a network.
import { sha256Hex } from "./hash.js";
import { createParser } from "./sse.js";

// postChat sends body and calls onEvent(name, data) for each event as it
// arrives. A non-200 reply throws an Error with the server's message and
// the HTTP status.
export async function postChat(body, onEvent, { fetchFn = fetch, signal } = {}) {
  const json = JSON.stringify(body);
  const resp = await fetchFn("/chat", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      // CloudFront's origin access control needs the hash of the exact body.
      "x-amz-content-sha256": await sha256Hex(json),
    },
    body: json,
    signal,
  });
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`;
    try {
      msg = (await resp.json()).error || msg;
    } catch { /* not JSON: keep the status */ }
    const err = new Error(msg);
    err.status = resp.status;
    throw err;
  }
  const parser = createParser(({ event, data }) => onEvent(event, JSON.parse(data)));
  const reader = resp.body.pipeThrough(new TextDecoderStream()).getReader();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    parser.feed(value);
  }
}
