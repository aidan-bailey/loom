// The palette's pure part: no React and no store. The score, the code-point order, and the items
// and their order from a snapshot, the registered commands and a command context.

/**
 * Orders two strings by code point: the first difference decides, and a string that is a prefix of
 * the other comes first. Never `localeCompare` (locale order) or `<`, which compares UTF-16 units,
 * where a surrogate (an astral code point) sorts below U+E000 to U+FFFF.
 */
export function compareCodePoints(a: string, b: string): number {
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    const x = a.codePointAt(i) ?? 0;
    const y = b.codePointAt(j) ?? 0;
    if (x !== y) return x - y;
    i += x > 0xffff ? 2 : 1;
    j += y > 0xffff ? 2 : 1;
  }
  if (i < a.length) return 1;
  if (j < b.length) return -1;
  return 0;
}

// startsWord: the code point at i begins a word, at the text's start or after one that is neither
// a letter nor a digit.
function startsWord(points: string[], i: number): boolean {
  if (i === 0) return true;
  const before = points[i - 1] ?? "";
  return !/\p{L}/u.test(before) && !/\p{N}/u.test(before);
}

/**
 * Scores a query against a text, or null when the query's code points do not all occur in the
 * text in order. Both are lowercased and the query trimmed; an empty query scores 0. An alignment
 * starts at a place in the text holding the query's first code point and takes each later one at
 * its next occurrence; each matched code point scores 1, plus 2 when it directly follows the
 * previous match, plus 3 when it starts a word. The result is the best over every start. The work
 * is quadratic only in the text's length, which is a session's title, branch or workspace name, a
 * command's title or a place's title, so no length bound is needed.
 */
export function fuzzyScore(query: string, text: string): number | null {
  const q = [...query.trim().toLowerCase()];
  if (q.length === 0) return 0;
  const t = [...text.toLowerCase()];
  let best: number | null = null;
  for (let start = 0; start < t.length; start++) {
    if (t[start] !== q[0]) continue;
    let score = 0;
    let prev = -2;
    let at = start;
    let ok = true;
    for (const point of q) {
      while (at < t.length && t[at] !== point) at++;
      if (at >= t.length) {
        ok = false;
        break;
      }
      score += 1 + (at === prev + 1 ? 2 : 0) + (startsWord(t, at) ? 3 : 0);
      prev = at;
      at++;
    }
    if (ok && (best === null || score > best)) best = score;
  }
  return best;
}
