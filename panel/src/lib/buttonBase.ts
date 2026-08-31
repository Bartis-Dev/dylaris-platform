// The button classes are a BASE plus modifiers, and the modifiers alone look
// broken.
//
// `.btn` carries everything that makes a button a button: inline-flex, the
// radius, the padding, the font size, the focus ring. `.btn-primary` and its
// siblings only add colour, and `.btn-icon` / `.btn-sm` only adjust padding. A
// `className="btn-primary"` with no `btn` therefore renders a square, unpadded
// block of accent colour hugging its text - which is what "the button looks
// cursed" turns out to mean every time.
//
// It is easy to write and impossible to see in review, because the class name
// reads like a complete thing. Measured when this was added: 442 modifier uses
// across the panel, 13 of them missing the base - and 9 of those 13 were the
// same page, every button on it.

const MODIFIERS = /\bbtn-(primary|secondary|danger|ghost|icon|sm|lg)\b/;
const BASE = /\bbtn(?![-\w])/;

// classStringsMissingBase returns each class string that names a button
// modifier without the base class.
//
// It reads the class strings rather than the file as a whole, and that is the
// whole point: a file-wide "does `btn` appear anywhere" check is green for a
// page whose every button is broken, as long as one other element has it.
export function classStringsMissingBase(source: string): string[] {
    const out: string[] = [];
    // Both spellings the panel uses: className="..." and className={`...`},
    // including the conditional halves inside a template literal.
    for (const m of source.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\}|\{'([^']*)'\})/g)) {
        const value = m[1] ?? m[2] ?? m[3] ?? '';
        if (!MODIFIERS.test(value)) continue;
        if (BASE.test(value)) continue;
        out.push(value);
    }
    return out;
}
