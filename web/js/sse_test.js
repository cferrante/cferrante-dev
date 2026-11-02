import { assertEquals } from "./testing.js";
import { createParser } from "./sse.js";

const stream = 'event: thought\ndata: {"seq":1,"text":"hi"}\n\n' +
  ": ping\n\n" +
  'event: token\ndata: {"seq":2,"text":"a\\nb"}\n\n' +
  "event: done\ndata: {}\n\n";

const want = [
  { event: "thought", data: '{"seq":1,"text":"hi"}' },
  { event: "token", data: '{"seq":2,"text":"a\\nb"}' },
  { event: "done", data: "{}" },
];

function parseAll(pieces) {
  const got = [];
  const p = createParser((e) => got.push(e));
  for (const piece of pieces) p.feed(piece);
  return got;
}

Deno.test("parses a whole stream and skips comments", () => {
  assertEquals(parseAll([stream]), want);
});

Deno.test("an event split at any byte is parsed once", () => {
  for (let i = 1; i < stream.length; i++) {
    assertEquals(parseAll([stream.slice(0, i), stream.slice(i)]), want, `split at ${i}`);
  }
});

Deno.test("one character at a time", () => {
  assertEquals(parseAll([...stream]), want);
});

Deno.test("CRLF line endings and multi-line data", () => {
  const got = parseAll(["event: x\r\ndata: one\r\ndata: two\r\n\r\n"]);
  assertEquals(got, [{ event: "x", data: "one\ntwo" }]);
});

Deno.test("an event without a name is a message; one without data is skipped", () => {
  assertEquals(parseAll(["data: 1\n\nevent: empty\n\n"]), [{ event: "message", data: "1" }]);
});
