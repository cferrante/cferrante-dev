import { assertEquals } from "./testing.js";
import { linkify, safeUrl, sources } from "./cite.js";

const cites = [
  { n: 1, chunk_id: "proj-alpha#intro", title: "Project Alpha", url: "/projects/alpha" },
  {
    n: 2,
    chunk_id: "proj-alpha#summary",
    title: "Project Alpha",
    url: "https://cferrante.dev/projects/alpha#summary",
  },
];

Deno.test("only same-site paths and https URLs are safe", () => {
  for (const u of ["/projects/alpha", "/notes#rates", "https://cferrante.dev/x"]) {
    assertEquals(safeUrl(u), true, u);
  }
  for (
    const u of [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "data:text/html,<script>",
      "http://example.org",
      "//evil.example/x",
      "/\\evil.example",
      "",
      "projects/alpha",
    ]
  ) {
    assertEquals(safeUrl(u), false, u);
  }
});

Deno.test("markers with a citation become links; the rest stays text", () => {
  assertEquals(linkify("Alpha is Go [1] and streams [2]. Not [3].", cites), [
    { text: "Alpha is Go " },
    { text: "[1]", href: "/projects/alpha", title: "Project Alpha" },
    { text: " and streams " },
    { text: "[2]", href: "https://cferrante.dev/projects/alpha#summary", title: "Project Alpha" },
    { text: ". Not [3]." },
  ]);
});

Deno.test("a citation with an unsafe URL renders as plain text", () => {
  const evil = [{ n: 1, chunk_id: "x", title: "Click me", url: "javascript:alert(document.cookie)" }];
  assertEquals(linkify("See [1].", evil), [{ text: "See [1]." }]);
});

Deno.test("comma lists link each number", () => {
  assertEquals(linkify("Both [1, 2].", cites), [
    { text: "Both [" },
    { text: "1", href: "/projects/alpha", title: "Project Alpha" },
    { text: ", " },
    { text: "2", href: "https://cferrante.dev/projects/alpha#summary", title: "Project Alpha" },
    { text: "]." },
  ]);
});

Deno.test("text without markers is one segment", () => {
  assertEquals(linkify("No sources here.", cites), [{ text: "No sources here." }]);
  assertEquals(linkify("", cites), []);
});

Deno.test("sources lists each citation once, in number order, safe URLs only", () => {
  const list = sources([cites[1], cites[0], cites[0], { n: 3, title: "Bad", url: "javascript:x" }]);
  assertEquals(list, [
    { n: 1, title: "Project Alpha", href: "/projects/alpha" },
    { n: 2, title: "Project Alpha", href: "https://cferrante.dev/projects/alpha#summary" },
    { n: 3, title: "Bad", href: "" },
  ]);
});
