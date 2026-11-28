// The terminal's buffer rows as the plain text the phone's copy sheet shows (spec §33, §56).

/** One row of the terminal's buffer as held, trailing blanks kept; `wrapped` means it continues the row before. */
export interface BufferLine {
  text: string;
  wrapped: boolean;
}

/**
 * The rows as plain text: a wrapped row joins the row before it untrimmed (so a row that the next
 * continues keeps its trailing blanks, §56), each logical line loses its trailing U+0020 blanks
 * once at its end, the empty lines at the end are dropped, and the lines are joined with "\n".
 * Reads `lines` once; a tab is not a blank cell, so it stays.
 */
export function plainText(lines: Iterable<BufferLine>): string {
  const logical: string[] = [];
  for (const line of lines) {
    if (line.wrapped && logical.length > 0) {
      logical[logical.length - 1] += line.text;
    } else {
      logical.push(line.text);
    }
  }
  const cut = logical.map((s) => s.replace(/ +$/, ""));
  let end = cut.length;
  while (end > 0 && cut[end - 1] === "") end--;
  return cut.slice(0, end).join("\n");
}
