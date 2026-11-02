// SHA-256 of a request body as lowercase hex, for CloudFront's
// x-amz-content-sha256 header (spike 1.4, design §14). Hashes the UTF-8
// bytes, which is what fetch sends for a string body.
export async function sha256Hex(text) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}
