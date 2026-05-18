import { StreamLanguage, LanguageSupport } from '@codemirror/language';
import type { StringStream } from '@codemirror/language';

// Minimal stream-based parser for .properties / .conf / .cfg style files:
//   # comment
//   ; comment   (some dialects)
//   key=value
//   key.with.dots=value with spaces
// Designed so all syntax-highlight tags map onto generic CodeMirror tokens
// (keyword/atom/string/comment) for theme compatibility.
const propertiesLang = StreamLanguage.define({
  name: 'properties',
  startState: () => ({ atKey: true }),
  token(stream: StringStream, state: { atKey: boolean }): string | null {
    if (stream.sol()) {
      state.atKey = true;
    }
    if (stream.eatSpace()) return null;

    // Comments — # or ; at start (after optional whitespace).
    if (state.atKey && (stream.peek() === '#' || stream.peek() === ';')) {
      stream.skipToEnd();
      return 'lineComment';
    }

    if (state.atKey) {
      // Key: everything until = or : or whitespace.
      if (stream.eatWhile(/[^=:\s]/)) {
        state.atKey = false;
        return 'propertyName';
      }
      // Lonely separator on its own line — skip.
      stream.next();
      return null;
    }

    // Separator
    if (stream.eat('=') || stream.eat(':')) {
      return 'operator';
    }

    // Boolean / number atoms get highlighted as such for visual scanability.
    const start = stream.pos;
    if (stream.match(/(true|false|on|off|yes|no)\b/i, true)) return 'atom';
    if (stream.match(/-?[0-9]+(\.[0-9]+)?\b/, true)) return 'number';
    stream.pos = start;

    // Value — everything to end of line is treated as a string literal.
    stream.skipToEnd();
    return 'string';
  },
});

export function propertiesLanguage(): LanguageSupport {
  return new LanguageSupport(propertiesLang);
}
