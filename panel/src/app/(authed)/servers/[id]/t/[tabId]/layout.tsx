// A server layout that exists for one reason: generateStaticParams cannot be
// exported from a client component, and the page below is one.
//
// It renders its children untouched, so it adds no markup and no boundary the
// browser can see. Without it the static export refuses the route outright.
import { EXPORT_PARAM } from '@/lib/exportParam';

/** One shell for the proxied custom tab; Core serves it for every real value. */
export function generateStaticParams() {
    return [{ tabId: EXPORT_PARAM }];
}

export default function Layout({ children }: { children: React.ReactNode }) {
    return children;
}
