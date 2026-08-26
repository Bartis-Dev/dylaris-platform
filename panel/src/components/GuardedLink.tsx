"use client";

import React, { useState } from 'react';
import Link, { LinkProps } from 'next/link';
import { useRouter, usePathname } from 'next/navigation';
import {
    useUnsavedChangesState,
    UnsavedDialog,
} from '@/components/settings/UnsavedChanges';

// GuardedLink behaves like next/link's <Link>, except that when any form on
// the current page has registered itself as dirty with the shared
// UnsavedChanges context, clicking it pops the Save / Discard / Cancel
// dialog instead of navigating immediately.
//
// When nothing is dirty (the common case) this is a transparent passthrough
// — no overhead, no behavior change. Wherever the app would normally use
// <Link>, swapping in <GuardedLink> is safe.

interface GuardedLinkProps extends Omit<LinkProps, 'onClick'> {
    children: React.ReactNode;
    className?: string;
    title?: string;
    target?: string;
    rel?: string;
    'aria-label'?: string;
}

export default function GuardedLink({ children, ...props }: GuardedLinkProps) {
    const registration = useUnsavedChangesState();
    const router = useRouter();
    const pathname = usePathname();
    const [pending, setPending] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);

    const hrefStr = typeof props.href === 'string' ? props.href : '';

    const handleClick = (e: React.MouseEvent<HTMLAnchorElement>) => {
        // Let the user open in a new tab / window — that's a fresh page
        // load, beforeunload covers it.
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        if (!registration?.dirty) return;
        // Navigating to the same path you're on isn't really "leaving"; skip
        // the guard so e.g. clicking the active tab stays a no-op.
        if (hrefStr && (pathname === hrefStr || pathname.startsWith(hrefStr + '/'))) {
            return;
        }
        e.preventDefault();
        setPending(hrefStr);
    };

    const navigate = (href: string) => {
        if (props.replace) router.replace(href);
        else router.push(href);
    };

    const close = () => {
        setPending(null);
        setSaving(false);
    };

    const handleSave = async () => {
        if (!registration || pending === null) return;
        setSaving(true);
        let ok = false;
        try {
            ok = await registration.save();
        } catch {
            ok = false;
        }
        setSaving(false);
        // Stay on the page unless the work is actually stored. The catch alone
        // was not enough: a refused save - a server error, or the operator
        // answering "no" to a confirmation - resolves normally, so this used to
        // navigate away from exactly the edits it was protecting.
        if (!ok) return;
        const href = pending;
        close();
        navigate(href);
    };

    const handleDiscard = () => {
        if (!registration || pending === null) return;
        registration.discard();
        const href = pending;
        close();
        navigate(href);
    };

    return (
        <>
            <Link {...props} onClick={handleClick}>
                {children}
            </Link>
            {pending !== null && registration && (
                <UnsavedDialog
                    onSave={handleSave}
                    onDiscard={handleDiscard}
                    onCancel={close}
                    saving={saving}
                />
            )}
        </>
    );
}
