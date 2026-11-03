import { assert, assertEquals } from "./testing.js";
import { postChat } from "./client.js";
import { sha256Hex } from "./hash.js";

// A fake fetch that records the request and streams the given chunks.
function fakeFetch(chunks, { status = 200, json } = {}) {
  const calls = [];
  const fn = (url, init) => {
    calls.push({ url, init });
    if (status !== 200) {
      return Promise.resolve(new Response(JSON.stringify(json), { status }));
    }
    const enc = new TextEncoder();
    const body = new ReadableStream({
      start(c) {
        for (const ch of chunks) c.enqueue(enc.encode(ch));
        c.close();
      },
    });
    return Promise.resolve(new Response(body, { headers: { "content-type": "text/event-stream" } }));
  };
  return { fn, calls };
}

Deno.test("posts JSON with the body hash and streams parsed events", async () => {
  const { fn, calls } = fakeFetch([
    'event: thought\ndata: {"text":"hm","so',
    'urce":"model"}\n\nevent: done\ndata: {"conversation_id":"c_9"}\n\n',
  ]);
  const got = [];
  const body = { conversation_id: "c_1", message: "héllo" };
  await postChat(body, (name, data) => got.push([name, data]), { fetchFn: fn });

  assertEquals(got, [["thought", { text: "hm", source: "model" }], ["done", { conversation_id: "c_9" }]]);
  assertEquals(calls.length, 1);
  const { url, init } = calls[0];
  assertEquals([url, init.method], ["/chat", "POST"]);
  const headers = new Headers(init.headers);
  assertEquals(headers.get("content-type"), "application/json");
  // CloudFront needs the hash of exactly the bytes sent.
  assertEquals(headers.get("x-amz-content-sha256"), await sha256Hex(init.body));
  assertEquals(JSON.parse(init.body), body);
});

Deno.test("a non-200 reply throws with the server's message", async () => {
  const { fn } = fakeFetch([], { status: 400, json: { error: "message is empty" } });
  let err;
  try {
    await postChat({ message: "" }, () => {}, { fetchFn: fn });
  } catch (e) {
    err = e;
  }
  assert(err, "want an error");
  assertEquals([err.message, err.status], ["message is empty", 400]);
});

Deno.test("multi-byte characters split across chunks decode correctly", async () => {
  // 'é' is C3 A9; split the two bytes across reads.
  const enc = new TextEncoder().encode('event: token\ndata: {"text":"é"}\n\n');
  const i = enc.indexOf(0xc3) + 1;
  const fn = () =>
    Promise.resolve(
      new Response(
        new ReadableStream({
          start(c) {
            c.enqueue(enc.slice(0, i));
            c.enqueue(enc.slice(i));
            c.close();
          },
        }),
      ),
    );
  const got = [];
  await postChat({ message: "x" }, (n, d) => got.push([n, d]), { fetchFn: fn });
  assertEquals(got, [["token", { text: "é" }]]);
});

Deno.test("getSuggested lists the chips, and an error gives none", async () => {
  const { getSuggested } = await import("./client.js");
  const ok = () => Promise.resolve(new Response('[{"id":"a","question":"Q?"}]'));
  assertEquals(await getSuggested({ fetchFn: ok }), [{ id: "a", question: "Q?" }]);
  const down = () => Promise.reject(new TypeError("Failed to fetch"));
  assertEquals(await getSuggested({ fetchFn: down }), []);
  const bad = () => Promise.resolve(new Response("nope", { status: 500 }));
  assertEquals(await getSuggested({ fetchFn: bad }), []);
});

// Stop aborts the request: that's the visitor's choice, not a failure, so it
// must not be shown as "can't reach the assistant" (step 6.0c).
Deno.test("an aborted request is no failure; others map to http or network", async () => {
  const { failureEvent } = await import("./client.js");
  const ctl = new AbortController();
  ctl.abort();
  let err;
  try {
    await postChat({ message: "hi" }, () => {}, {
      fetchFn: (_u, init) => fetch("data:,", init),
      signal: ctl.signal,
    });
  } catch (e) {
    err = e;
  }
  assert(err, "want the abort to reject");
  assertEquals(failureEvent(err), null);
  const http = new Error("bad gateway");
  http.status = 502;
  assertEquals(failureEvent(http), { code: "http", status: 502, message: "bad gateway" });
  assertEquals(failureEvent(new TypeError("Failed to fetch")), {
    code: "network",
    status: undefined,
    message: "Failed to fetch",
  });
});
