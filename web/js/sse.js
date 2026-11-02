// Server-Sent Events parser (design §3, §14). Feed it decoded text in
// whatever pieces the network delivers; it calls onEvent({event, data}) once
// per complete event, however the event was split. Comments (": ping") and
// events without data are skipped.
export function createParser(onEvent) {
  let buf = "";
  let event = "";
  let data = [];

  function line(l) {
    if (l === "") { // a blank line ends the event
      if (data.length > 0) onEvent({ event: event || "message", data: data.join("\n") });
      event = "";
      data = [];
      return;
    }
    if (l.startsWith(":")) return; // comment, e.g. the keep-alive
    const colon = l.indexOf(":");
    const field = colon < 0 ? l : l.slice(0, colon);
    let value = colon < 0 ? "" : l.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value;
    else if (field === "data") data.push(value);
  }

  return {
    feed(text) {
      buf += text;
      let nl;
      // Only complete lines are processed; the rest waits for the next piece.
      while ((nl = buf.indexOf("\n")) >= 0) {
        let l = buf.slice(0, nl);
        if (l.endsWith("\r")) l = l.slice(0, -1);
        buf = buf.slice(nl + 1);
        line(l);
      }
    },
  };
}
