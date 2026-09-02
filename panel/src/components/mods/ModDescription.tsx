import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';

// The long description of a Modrinth project, rendered.
//
// A Modrinth body is Markdown that embeds raw HTML - the badge row, the
// screenshots and the add-on tables of most large mods are written as <div>,
// <a> and <img>. react-markdown drops raw HTML unless rehype-raw is asked for
// it, and it does not drop it silently: it printed the tags as visible text, so
// the description of a mod like Plasmo Voice opened with a wall of markup.
//
// rehype-sanitize is what makes that safe and is not optional here. The body is
// written by the mod author and renders on the panel origin, where a script tag
// would run as the panel. Its default schema is an allowlist that already
// permits everything a description needs - div, img, tables, links, http and
// https only - and no style, no event handler and no script, so it is used
// as-is rather than extended. Extending it is the only way to get this wrong,
// so the absence of a custom schema is the point.
//
// It lives in its own file so the pipeline can be rendered in a test without
// the content page around it; see ModDescription.test.ts.
export function ModDescription({ body }: { body: string }) {
    return (
        <div className="text-sm text-(--base-08) leading-relaxed wrap-break-word [&_h1]:text-base [&_h1]:font-semibold [&_h1]:text-(--base-09) [&_h1]:mt-3 [&_h1]:mb-1 [&_h2]:text-sm [&_h2]:font-semibold [&_h2]:text-(--base-09) [&_h2]:mt-3 [&_h2]:mb-1 [&_h3]:font-semibold [&_h3]:text-(--base-09) [&_h3]:mt-2 [&_p]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ul]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_ol]:my-2 [&_a]:text-(--accent-light) [&_a]:underline [&_code]:font-mono [&_code]:text-xs [&_code]:bg-(--base-03) [&_code]:px-1 [&_code]:rounded [&_pre]:bg-(--base-01) [&_pre]:p-2 [&_pre]:rounded [&_pre]:overflow-x-auto [&_pre]:my-2 [&_img]:max-w-full [&_img]:rounded [&_img]:my-2 [&_hr]:my-3 [&_hr]:border-(--base-03) [&_blockquote]:border-l-2 [&_blockquote]:border-(--base-04) [&_blockquote]:pl-3 [&_blockquote]:text-(--base-06) [&_table]:text-xs [&_table]:block [&_table]:overflow-x-auto [&_td]:align-top [&_td]:py-1 [&_td]:pr-3 [&_th]:text-left [&_th]:py-1 [&_th]:pr-3 [&_th]:text-(--base-09)">
            <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                rehypePlugins={[rehypeRaw, rehypeSanitize]}
                components={{
                    a: ({ node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
                    // The author picks these hosts, so the request must carry as
                    // little as possible: no referrer, and nothing fetched until
                    // the reader scrolls that far.
                    // eslint-disable-next-line @next/next/no-img-element
                    img: ({ node, ...props }) => <img {...props} alt={props.alt ?? ''} loading="lazy" referrerPolicy="no-referrer" />,
                }}
            >
                {body}
            </ReactMarkdown>
        </div>
    );
}
