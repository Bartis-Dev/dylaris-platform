'use client';

import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { HelpCircle } from 'lucide-react';

/**
 * Field-level help.
 *
 * The panel had no help of its own: 28 files leaned on the native `title`
 * attribute, which is a browser tooltip with a delay, no keyboard route to it,
 * no styling, and no way to hold a second line or an example. Several of the
 * values it was used on - beam throttles, warp connection limits, an S3
 * endpoint - cannot be guessed from their label at all.
 *
 * Portalled and fixed-positioned on purpose: every settings page scrolls inside
 * an overflow container, and an absolutely positioned popover would be clipped
 * by it.
 */

export interface HelpPlacement {
    left: number;
    top: number;
}

/**
 * place decides where the popover sits: to the right of the trigger if it fits,
 * otherwise to its left, and clamped so it can never render off-screen.
 *
 * Pure and exported for its own test. Getting it wrong is silent - the popover
 * opens somewhere unhelpful rather than throwing - and the failing case is a
 * narrow window, which is not the window anyone develops in.
 */
export function place(
    trigger: { left: number; right: number; top: number },
    pop: { width: number; height: number },
    viewport: { width: number; height: number },
    gap = 8,
): HelpPlacement {
    const margin = 8;

    let left = trigger.right + gap;
    if (left + pop.width > viewport.width - margin) {
        left = trigger.left - gap - pop.width;
    }
    left = Math.max(margin, Math.min(left, viewport.width - pop.width - margin));

    // Vertically centred on the trigger row, then pushed back inside.
    let top = trigger.top - 8;
    top = Math.max(margin, Math.min(top, viewport.height - pop.height - margin));

    return { left, top };
}

export default function HelpTip({
    children,
    label = 'Help',
    className = '',
}: {
    /** The help text. Rich content is fine: <code>, <strong>, a second line. */
    children: React.ReactNode;
    label?: string;
    className?: string;
}) {
    const [open, setOpen] = useState(false);
    const [pos, setPos] = useState<HelpPlacement | null>(null);
    const btnRef = useRef<HTMLButtonElement>(null);
    const popRef = useRef<HTMLDivElement>(null);

    useLayoutEffect(() => {
        if (!open || !btnRef.current || !popRef.current) return;
        const t = btnRef.current.getBoundingClientRect();
        const p = popRef.current.getBoundingClientRect();
        setPos(
            place(
                { left: t.left, right: t.right, top: t.top },
                { width: p.width, height: p.height },
                { width: window.innerWidth, height: window.innerHeight },
            ),
        );
    }, [open, children]);

    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') setOpen(false);
        };
        const onDown = (e: MouseEvent) => {
            const target = e.target as Node;
            if (btnRef.current?.contains(target) || popRef.current?.contains(target)) return;
            setOpen(false);
        };
        // Scrolling moves the trigger out from under a fixed popover, so the
        // popover closes rather than floating over an unrelated field.
        const onScroll = () => setOpen(false);
        document.addEventListener('keydown', onKey);
        document.addEventListener('mousedown', onDown);
        window.addEventListener('scroll', onScroll, true);
        window.addEventListener('resize', onScroll);
        return () => {
            document.removeEventListener('keydown', onKey);
            document.removeEventListener('mousedown', onDown);
            window.removeEventListener('scroll', onScroll, true);
            window.removeEventListener('resize', onScroll);
        };
    }, [open]);

    return (
        <>
            <button
                ref={btnRef}
                type="button"
                aria-label={label}
                aria-expanded={open}
                onClick={() => setOpen(o => !o)}
                className={`help-trigger ${className}`}
            >
                <HelpCircle size={14} />
            </button>
            {open &&
                typeof document !== 'undefined' &&
                createPortal(
                    <div
                        ref={popRef}
                        role="tooltip"
                        className="help-pop"
                        // Rendered off-screen for one frame while it is measured;
                        // otherwise it flashes at the top-left corner first.
                        style={
                            pos
                                ? { left: pos.left, top: pos.top }
                                : { left: -9999, top: -9999 }
                        }
                    >
                        {children}
                    </div>,
                    document.body,
                )}
        </>
    );
}
