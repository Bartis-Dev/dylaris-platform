"use client";

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';

// Protected addresses are a tab on /nodes now, next to the machines they run
// on. This route stays as a redirect: it was linked from the deploy snippets,
// the store copy and anything a customer bookmarked while it was its own page.
export default function RoutesRedirect() {
    const router = useRouter();
    useEffect(() => { router.replace('/nodes?tab=routes'); }, [router]);
    return null;
}
