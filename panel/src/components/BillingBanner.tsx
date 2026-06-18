"use client";

import React, { useEffect, useState } from 'react';
import { getMyBilling, BillingStatus } from '@/lib/api/billing';
import { AlertTriangle, ArrowRight } from 'lucide-react';

// BillingBanner is the non-dismissible red bar shown to a tenant in past_due or
// suspended. Clicking it goes to the payment URL. Renders nothing when active,
// so it is a no-op for solo/hoster users and paying tenants. Re-polls every 5
// minutes so a status change surfaces without a reload.
export default function BillingBanner() {
    const [status, setStatus] = useState<BillingStatus | null>(null);
    const [graceUntil, setGraceUntil] = useState<string | null>(null);
    const [paymentUrl, setPaymentUrl] = useState('');

    useEffect(() => {
        let cancelled = false;
        const load = async () => {
            const res = await getMyBilling();
            if (cancelled || !res.success) return;
            setStatus(res.status || null);
            setGraceUntil(res.graceUntil || null);
            setPaymentUrl(res.paymentUrl || '');
        };
        load();
        const t = setInterval(load, 5 * 60 * 1000);
        return () => { cancelled = true; clearInterval(t); };
    }, []);

    if (!status || status === 'active') return null;

    const suspended = status === 'suspended';
    const message = suspended
        ? 'Your services are suspended for non-payment. Your data and backups are safe — settle payment to reactivate.'
        : `Payment required. Your services will be suspended${graceUntil ? ` on ${new Date(graceUntil).toLocaleString()}` : ''}. Pay now to avoid interruption.`;

    const inner = (
        <div className="flex items-center justify-center gap-3 bg-(--error) text-white px-4 py-2.5 text-sm font-medium shadow-md">
            <AlertTriangle size={18} className="shrink-0" />
            <span>{message}</span>
            {paymentUrl && (
                <span className="inline-flex items-center gap-1 font-semibold underline underline-offset-2 whitespace-nowrap">
                    Pay now <ArrowRight size={15} />
                </span>
            )}
        </div>
    );

    // Whole bar is the call-to-action. No dismiss control by design.
    if (paymentUrl) {
        return (
            <a href={paymentUrl} target="_blank" rel="noreferrer" className="block shrink-0 hover:brightness-110 transition-[filter]">
                {inner}
            </a>
        );
    }
    return <div className="shrink-0">{inner}</div>;
}
