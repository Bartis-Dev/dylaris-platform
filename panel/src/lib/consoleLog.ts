// Level-based colouring for server console lines.
//
// Minecraft/Java servers usually log as `[HH:MM:SS] [Thread/LEVEL]: message`,
// and many emit it as plain text with no ANSI colour codes, so every line
// renders in the same colour. This maps the detected log level to a design
// token so warnings and errors stand out. ANSI-coloured segments still win,
// because parseAnsiLine sets inline colours on their spans, which override the
// line's className. Lines with no recognised level keep the default colour.

export function logLineClass(line: string): string {
    const lvl = (line.match(/\b(ERROR|SEVERE|FATAL|WARN|WARNING|DEBUG|TRACE)\b/i)?.[1] || '').toUpperCase();
    if (lvl === 'ERROR' || lvl === 'SEVERE' || lvl === 'FATAL') return 'text-(--error-light)';
    if (lvl === 'WARN' || lvl === 'WARNING') return 'text-(--warning-light)';
    if (lvl === 'DEBUG' || lvl === 'TRACE') return 'text-(--base-06)';
    return 'text-(--base-09)';
}
