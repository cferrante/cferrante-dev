// Minimal assertions, so tests import nothing from the network (Deno runs
// them with no permissions at all).
export function assertEquals(actual, expected, msg = "") {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${msg ? msg + ": " : ""}\n  got  ${a}\n  want ${e}`);
}

export function assert(cond, msg = "assertion failed") {
  if (!cond) throw new Error(msg);
}
