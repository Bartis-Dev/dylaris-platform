// Level-based colouring for server console lines.
//
// Minecraft/Java servers usually log as `[HH:MM:SS] [Thread/LEVEL]: message`,
// and many emit it as plain text with no ANSI colour codes, so every line
// renders in the same colour. This maps the detected log level to a design
// token so warnings and errors stand out. ANSI-coloured segments still win,
// because parseAnsiLine sets inline colours on their spans, which override the
// line's className. Lines with no recognised level keep the default colour.
//
// Continuation lines (an exception header + `at ...` stack frames following
// an ERROR record) carry no level token of their own, so they inherit the
// level of the record they belong to - see computeLineLevels.

export type Level = 'error' | 'warn' | 'debug' | 'info';

const DEFAULT_LEVEL: Level = 'info';

// Pure regex detection only - no default fallback. Returns null when the
// line matches no known level token.
export function detectLevel(line: string): Level | null {
    const lvl = (line.match(/\b(ERROR|SEVERE|FATAL|WARN|WARNING|DEBUG|TRACE)\b/i)?.[1] || '').toUpperCase();
    if (lvl === 'ERROR' || lvl === 'SEVERE' || lvl === 'FATAL') return 'error';
    if (lvl === 'WARN' || lvl === 'WARNING') return 'warn';
    if (lvl === 'DEBUG' || lvl === 'TRACE') return 'debug';
    return null;
}

export function levelClass(level: Level): string {
    if (level === 'error') return 'text-(--error-light)';
    if (level === 'warn') return 'text-(--warning-light)';
    if (level === 'debug') return 'text-(--base-06)';
    return 'text-(--base-09)';
}

export function logLineClass(line: string): string {
    return levelClass(detectLevel(line) ?? DEFAULT_LEVEL);
}

const TIMESTAMP_PREFIX = /^\[\d{2}:\d{2}:\d{2}\]/;
const STACK_FRAME = /^\s*at\s/;
const EXCEPTION_SHAPE = /^[\w.$]+(Exception|Error)(:|$)/;

// A line with no level token of its own is a continuation of the previous
// record (exception header / `at ...` stack frame) rather than a fresh
// default-level record when it looks like one of these shapes, or - the
// broadest signal - simply lacks the `[HH:MM:SS]` timestamp prefix that real
// log records start with.
function isContinuationLine(line: string): boolean {
    return /^\s/.test(line)
        || STACK_FRAME.test(line)
        || EXCEPTION_SHAPE.test(line)
        || !TIMESTAMP_PREFIX.test(line);
}

// Single-pass fold over a batch of console lines: lines that match a level
// keep it, continuation-shaped lines inherit the previous line's computed
// level, everything else falls back to the default level.
export function computeLineLevels(lines: string[]): Level[] {
    const levels: Level[] = [];
    let previous: Level = DEFAULT_LEVEL;
    for (const line of lines) {
        const detected = detectLevel(line);
        const level = detected ?? (isContinuationLine(line) ? previous : DEFAULT_LEVEL);
        levels.push(level);
        previous = level;
    }
    return levels;
}
