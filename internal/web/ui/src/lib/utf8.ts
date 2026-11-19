// MAX_MESSAGE_BYTES is the limit, in UTF-8 bytes, the daemon holds a reply, an
// answer's text and a message to (64 KiB, as the classic screens' limit).
export const MAX_MESSAGE_BYTES = 65536;

const encoder = new TextEncoder();

// utf8Length is the number of bytes text takes encoded as UTF-8, which is what
// the daemon counts: not text.length, which counts UTF-16 units.
export function utf8Length(text: string): number {
  return encoder.encode(text).length;
}
