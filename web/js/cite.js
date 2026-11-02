// Citations in an answer (design §3, §14): [n] markers become links only when
// the server sent a citation for n with a safe URL; everything else is text.
// The page builds links from these segments with textContent and href, never
// from HTML, so a URL like "javascript:..." can't run.

// safeUrl allows same-site paths ("/x", but not "//host" or "/\host", which
// browsers treat as another site) and https URLs only.
export function safeUrl(url) {
  if (typeof url !== "string" || url === "") return false;
  if (url.startsWith("/")) return !url.startsWith("//") && !url.startsWith("/\\");
  try {
    return new URL(url).protocol === "https:";
  } catch {
    return false;
  }
}

const MARKER = /\[(\d+(?:\s*,\s*\d+)*)\]/g;

// linkify splits answer text into segments: {text} or {text, href, title}.
// Adjacent plain text is merged into one segment.
export function linkify(answer, citations) {
  const byN = new Map(citations.filter((c) => safeUrl(c.url)).map((c) => [c.n, c]));
  const out = [];
  const text = (t) => {
    if (!t) return;
    const last = out[out.length - 1];
    if (last && last.href === undefined) last.text += t;
    else out.push({ text: t });
  };
  let at = 0;
  for (const m of answer.matchAll(MARKER)) {
    text(answer.slice(at, m.index));
    const nums = m[1].split(",").map((n) => n.trim());
    if (!nums.some((n) => byN.has(Number(n)))) {
      text(m[0]); // no linkable citation: keep the marker as written
    } else if (nums.length === 1) {
      const c = byN.get(Number(nums[0]));
      out.push({ text: m[0], href: c.url, title: c.title }); // the whole "[1]" is the link
    } else {
      text("[");
      // Keep the original separators between numbers.
      const parts = m[1].split(/(\s*,\s*)/);
      for (const part of parts) {
        const c = byN.get(Number(part));
        if (/^\d+$/.test(part) && c) out.push({ text: part, href: c.url, title: c.title });
        else text(part);
      }
      text("]");
    }
    at = m.index + m[0].length;
  }
  text(answer.slice(at));
  return out;
}

// sources lists the cited documents once each, by number; href is "" for an
// unsafe URL, so the page shows the title as text.
export function sources(citations) {
  const byN = new Map();
  for (const c of citations) if (!byN.has(c.n)) byN.set(c.n, c);
  return [...byN.values()].sort((a, b) => a.n - b.n)
    .map((c) => ({ n: c.n, title: c.title, href: safeUrl(c.url) ? c.url : "" }));
}
