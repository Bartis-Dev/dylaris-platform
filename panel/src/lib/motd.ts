// Minecraft MOTD formatting: the colour/style code vocabulary, and the parser
// that turns a raw motd= value into something previewable.
//
// This lives outside the page because it is the only part of the editor that
// can be wrong in a way nobody notices: the preview is what a server owner
// trusts instead of restarting and looking at the multiplayer list.

export interface MotdColor {
    /** The code character, e.g. 'a'. */
    code: string;
    /** Human name, used as the swatch tooltip. */
    name: string;
    hex: string;
}

/**
 * The sixteen colours, in the game's own order (dark row then bright row) so
 * the palette matches what a server owner has seen everywhere else.
 */
export const MOTD_COLORS: MotdColor[] = [
    { code: '0', name: 'Black', hex: '#000000' },
    { code: '1', name: 'Dark Blue', hex: '#0000AA' },
    { code: '2', name: 'Dark Green', hex: '#00AA00' },
    { code: '3', name: 'Dark Aqua', hex: '#00AAAA' },
    { code: '4', name: 'Dark Red', hex: '#AA0000' },
    { code: '5', name: 'Dark Purple', hex: '#AA00AA' },
    { code: '6', name: 'Gold', hex: '#FFAA00' },
    { code: '7', name: 'Gray', hex: '#AAAAAA' },
    { code: '8', name: 'Dark Gray', hex: '#555555' },
    { code: '9', name: 'Blue', hex: '#5555FF' },
    { code: 'a', name: 'Green', hex: '#55FF55' },
    { code: 'b', name: 'Aqua', hex: '#55FFFF' },
    { code: 'c', name: 'Red', hex: '#FF5555' },
    { code: 'd', name: 'Light Purple', hex: '#FF55FF' },
    { code: 'e', name: 'Yellow', hex: '#FFFF55' },
    { code: 'f', name: 'White', hex: '#FFFFFF' },
];

export const COLOR_MAP: Record<string, string> = Object.fromEntries(
    MOTD_COLORS.map(c => [c.code, c.hex]),
);

export interface MotdStyle {
    code: string;
    name: string;
    /** Short hint shown under the toolbar button. */
    hint: string;
}

export const MOTD_STYLES: MotdStyle[] = [
    { code: 'l', name: 'Bold', hint: 'Bold' },
    { code: 'o', name: 'Italic', hint: 'Italic' },
    { code: 'n', name: 'Underline', hint: 'Underline' },
    { code: 'm', name: 'Strikethrough', hint: 'Strike' },
    { code: 'k', name: 'Obfuscated', hint: 'Scrambles the characters in-game' },
    { code: 'r', name: 'Reset', hint: 'Back to plain white' },
];

export interface ColoredSegment {
    text: string;
    color: string;
    bold?: boolean;
    italic?: boolean;
    underline?: boolean;
    strike?: boolean;
    /** &k. The game scrambles these characters continuously. */
    obfuscated?: boolean;
}

/** The server list shows two lines. */
export const MOTD_MAX_LINES = 2;

/**
 * Characters that fit on one line of the multiplayer server list before the
 * client truncates. Advisory - the game measures pixels, not characters, so a
 * line of Ws runs out sooner than a line of dots.
 */
export const MOTD_SOFT_LINE_LIMIT = 59;

/**
 * Parses a raw motd value into per-line styled segments.
 *
 * A COLOUR code clears every active style, exactly as the game does: writing
 * "&lBold &ade-bolded" leaves the second half plain, and a preview that kept
 * the bold would quietly disagree with the server. A STYLE code keeps the
 * current colour and adds to what is already set, so styles stack.
 *
 * Both & and the literal section sign are accepted, because server.properties
 * files in the wild contain both.
 */
export function parseMotd(raw: string): ColoredSegment[][] {
    const lines = raw.split('\n').slice(0, MOTD_MAX_LINES);
    return lines.map(line => {
        const segments: ColoredSegment[] = [];
        let current: ColoredSegment = { text: '', color: '#FFFFFF' };

        const push = () => {
            if (current.text) segments.push(current);
        };

        for (let i = 0; i < line.length; i++) {
            const ch = line[i];
            if ((ch === '&' || ch === '§') && i + 1 < line.length) {
                const code = line[i + 1].toLowerCase();
                if (code in COLOR_MAP) {
                    push();
                    // Deliberately NOT spread from current: a colour resets
                    // bold/italic/underline/strike/obfuscated in-game.
                    current = { text: '', color: COLOR_MAP[code] };
                    i++;
                    continue;
                }
                if (code === 'r') {
                    push();
                    current = { text: '', color: '#FFFFFF' };
                    i++;
                    continue;
                }
                const style = (
                    { l: 'bold', o: 'italic', n: 'underline', m: 'strike', k: 'obfuscated' } as const
                )[code as 'l' | 'o' | 'n' | 'm' | 'k'];
                if (style) {
                    push();
                    current = { ...current, text: '', [style]: true };
                    i++;
                    continue;
                }
                // An unknown code is literal text. "Tom & Jerry" is a MOTD, not
                // a formatting error, and swallowing the & would lose a
                // character the server will actually display.
            }
            current.text += ch;
        }
        push();
        return segments;
    });
}

/** Length of each line ignoring the formatting codes, which are not drawn. */
export function motdVisibleLengths(raw: string): number[] {
    return raw
        .split('\n')
        .slice(0, MOTD_MAX_LINES)
        .map(line => parseMotd(line)[0]?.reduce((n, s) => n + s.text.length, 0) ?? 0);
}

/**
 * Inserts a formatting code at the caret and returns the new value plus where
 * the caret should land.
 *
 * The caret has to move past what was inserted, or a run of clicks builds the
 * code sequence backwards ("&c" then "&l" giving "&l&c").
 */
export function insertMotdCode(value: string, caret: number, code: string): { value: string; caret: number } {
    const at = Math.max(0, Math.min(caret, value.length));
    const token = `&${code}`;
    return {
        value: value.slice(0, at) + token + value.slice(at),
        caret: at + token.length,
    };
}
