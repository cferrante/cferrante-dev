import { assertEquals } from "./testing.js";
import { sha256Hex } from "./hash.js";

Deno.test("SHA-256 of a known string", async () => {
  // The FIPS 180-2 test vector for "abc".
  assertEquals(await sha256Hex("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
});

Deno.test("hashes UTF-8 bytes, not UTF-16 code units", async () => {
  // printf 'é' | sha256sum  (two UTF-8 bytes, C3 A9)
  assertEquals(await sha256Hex("é"), "4a99557e4033c3539de2eb65472017cad5f9557f7a0625a09f1c3f6e2ba69c4c");
});
