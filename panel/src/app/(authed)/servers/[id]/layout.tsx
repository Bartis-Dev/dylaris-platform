// Server component wrapper. The chrome lives in ServerShell (a client
// component); this file exists so generateStaticParams can be exported, which
// the static export requires for a dynamic segment and which a client component
// cannot do.
import { EXPORT_PARAM } from '@/lib/exportParam';
import ServerShell from './ServerShell';

/**
 * One shell for every server. Server ids are runtime data, so there is nothing
 * else to return; Core serves this file for /servers/1, /servers/742 and the
 * rest, and the client reads the real id from the URL via useParams.
 *
 * It covers the whole subtree - overview, files, console, config/*, t/[tabId] -
 * because generateStaticParams on a segment answers for its descendants too.
 */
export function generateStaticParams() {
    return [{ id: EXPORT_PARAM }];
}

export default function Layout({ children }: { children: React.ReactNode }) {
    return <ServerShell>{children}</ServerShell>;
}
