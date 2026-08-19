import { describe, it, expect } from 'vitest';
import {
    parseMotd, motdVisibleLengths, insertMotdCode,
    MOTD_COLORS, MOTD_MAX_LINES,
} from './motd';

describe('parseMotd', () => {
    it('leaves plain text as one white segment', () => {
        expect(parseMotd('A Minecraft Server')).toEqual([
            [{ text: 'A Minecraft Server', color: '#FFFFFF' }],
        ]);
    });

    it('applies a colour code', () => {
        const [line] = parseMotd('&aGreen');
        expect(line).toEqual([{ text: 'Green', color: '#55FF55' }]);
    });

    it('accepts the section sign as well as &', () => {
        expect(parseMotd('§cRed')[0]).toEqual([{ text: 'Red', color: '#FF5555' }]);
    });

    // The one that would quietly disagree with the server: in-game a colour
    // code clears bold/italic/underline/strike, so a preview that kept them
    // shows something the player will never see.
    it('a colour clears the active styles', () => {
        const [line] = parseMotd('&lBold &agreen');
        expect(line[0]).toMatchObject({ text: 'Bold ', bold: true });
        expect(line[1]).toEqual({ text: 'green', color: '#55FF55' });
    });

    it('styles stack and keep the current colour', () => {
        const [line] = parseMotd('&c&l&nLoud');
        expect(line[0]).toMatchObject({
            text: 'Loud', color: '#FF5555', bold: true, underline: true,
        });
    });

    it('understands the obfuscated code', () => {
        expect(parseMotd('&kxyz')[0][0]).toMatchObject({ text: 'xyz', obfuscated: true });
    });

    it('reset returns to plain white', () => {
        const [line] = parseMotd('&c&lLoud&rplain');
        expect(line[1]).toEqual({ text: 'plain', color: '#FFFFFF' });
    });

    // "Tom & Jerry" is a MOTD, not a formatting error. Swallowing the & would
    // drop a character the server actually displays.
    it('keeps an unknown code as literal text', () => {
        expect(parseMotd('Tom & Jerry')[0]).toEqual([{ text: 'Tom & Jerry', color: '#FFFFFF' }]);
    });

    it('keeps a trailing & that has nothing after it', () => {
        expect(parseMotd('Sale &')[0]).toEqual([{ text: 'Sale &', color: '#FFFFFF' }]);
    });

    // The server list has room for two.
    it('drops anything past the second line', () => {
        expect(parseMotd('one\ntwo\nthree')).toHaveLength(MOTD_MAX_LINES);
    });

    it('offers all sixteen colours', () => {
        expect(MOTD_COLORS).toHaveLength(16);
        expect(new Set(MOTD_COLORS.map(c => c.code)).size).toBe(16);
    });
});

describe('motdVisibleLengths', () => {
    // Codes are not drawn, so counting raw characters tells the owner their
    // line is too long when it is not.
    it('counts drawn characters, not codes', () => {
        expect(motdVisibleLengths('&c&lHello')).toEqual([5]);
    });

    it('counts each line separately', () => {
        expect(motdVisibleLengths('&aone\n&btwo!')).toEqual([3, 4]);
    });

    it('is 0 for an empty line', () => {
        expect(motdVisibleLengths('')).toEqual([0]);
    });
});

describe('insertMotdCode', () => {
    it('inserts at the caret and moves past what it inserted', () => {
        expect(insertMotdCode('Hello', 5, 'c')).toEqual({ value: 'Hello&c', caret: 7 });
    });

    // Without the caret advancing, a run of clicks builds the sequence
    // backwards: &c then &l gives "&l&c".
    it('keeps consecutive clicks in the order they were made', () => {
        const first = insertMotdCode('', 0, 'c');
        const second = insertMotdCode(first.value, first.caret, 'l');
        expect(second.value).toBe('&c&l');
    });

    it('inserts mid-string', () => {
        expect(insertMotdCode('AB', 1, 'a').value).toBe('A&aB');
    });

    it('clamps a caret outside the value', () => {
        expect(insertMotdCode('AB', 99, 'a').value).toBe('AB&a');
        expect(insertMotdCode('AB', -5, 'a').value).toBe('&aAB');
    });
});
