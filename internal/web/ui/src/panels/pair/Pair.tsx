// PAIRING_ALPHABET is the daemon's own pairing alphabet (internal/web/auth.go): 31 characters, no
// 0, 1, I, L or O, so a code read off a screen has no look-alikes.
export const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ";

// CODE_LENGTH is the length of a pairing code.
export const CODE_LENGTH = 8;

// normalizeCode upper-cases value, keeps only the characters the pairing alphabet holds, and keeps
// at most CODE_LENGTH of them. The cut follows the filter, so a dropped character costs no slot,
// and a mistyped O or 0 never reaches the daemon, which counts 5 wrong codes before it locks.
// It is idempotent, because OTPField normalises the same value more than once.
export function normalizeCode(value: string): string {
  let out = "";
  for (const ch of value.toUpperCase()) {
    if (PAIRING_ALPHABET.includes(ch)) out += ch;
  }
  return out.slice(0, CODE_LENGTH);
}
