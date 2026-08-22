"use client";

import { useCallback, useEffect, useState } from 'react';
import { Globe, ShieldCheck, Clock, AlertTriangle, Check, Copy } from 'lucide-react';
import {
  listCustomDomains,
  issueCustomDomainToken,
  verifyCustomDomainTXT,
  type CustomDomain,
} from '@/lib/api/customDomains';

// Plain language per state. The customer needs to know what to DO, not what the
// state machine calls itself.
function describe(d: CustomDomain, cnameTarget?: string): { tone: string; title: string; body: string } {
  switch (d.state) {
    case 'verified':
      return {
        tone: 'text-(--success)',
        title: 'Verified',
        body: 'This domain points at us. You can add routes on it.',
      };
    case 'pending':
      return {
        tone: 'text-(--warning)',
        title: 'Waiting for DNS',
        body: cnameTarget
          ? `Add a CNAME to ${cnameTarget}, or an A record to one of our edge addresses. We check every 30 minutes.`
          : 'Point this domain at us. We check every 30 minutes.',
      };
    case 'blocked':
      return {
        tone: 'text-(--warning)',
        title: 'Not set up in time',
        body: 'The route was removed. You can try again - set the record first, then add the route.',
      };
    default:
      return {
        tone: 'text-(--error)',
        title: 'Blocked',
        body: 'Too many failed checks. Publish the TXT record below to prove you own this domain.',
      };
  }
}

function timeLeft(deadlineAt?: string): string | null {
  if (!deadlineAt) return null;
  const ms = Date.parse(deadlineAt) - Date.now();
  if (Number.isNaN(ms) || ms <= 0) return null;
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  return h > 0 ? `${h}h ${m}m left` : `${m}m left`;
}

export function CustomDomainsPanel({ cnameTarget }: { cnameTarget?: string }) {
  const [domains, setDomains] = useState<CustomDomain[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [note, setNote] = useState<{ domain: string; text: string; ok: boolean } | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setDomains(await listCustomDomains());
    } catch {
      setDomains([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const getToken = async (domain: string) => {
    setBusy(domain);
    try {
      await issueCustomDomainToken(domain);
      await load();
    } finally {
      setBusy(null);
    }
  };

  const verify = async (domain: string) => {
    setBusy(domain);
    setNote(null);
    try {
      const res = await verifyCustomDomainTXT(domain);
      setNote({ domain, ok: true, text: res?.message ?? 'Verified.' });
      await load();
    } catch (e) {
      setNote({
        domain,
        ok: false,
        text: e instanceof Error ? e.message : 'The record was not found yet.',
      });
    } finally {
      setBusy(null);
    }
  };

  const copy = (value: string) => {
    navigator.clipboard.writeText(value).then(() => {
      setCopied(value);
      setTimeout(() => setCopied(null), 1600);
    });
  };

  if (domains === null) {
    return <div className="h-24 rounded-md bg-(--base-02) animate-pulse" aria-hidden />;
  }
  if (domains.length === 0) return null;

  return (
    <div className="space-y-3 rounded-md border border-(--base-03) bg-(--base-01) p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-(--base-09)">
        <Globe size={15} className="text-(--accent-light)" />
        Your own domains
      </div>

      <ul className="space-y-3">
        {domains.map((d) => {
          const info = describe(d, cnameTarget);
          const left = d.state === 'pending' ? timeLeft(d.deadlineAt) : null;
          return (
            <li key={d.domain} className="rounded-md border border-(--base-03) bg-(--base-02) p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs text-(--base-09)">{d.domain}</span>
                <span className={`inline-flex items-center gap-1 text-xs ${info.tone}`}>
                  {d.state === 'verified' ? <ShieldCheck size={13} /> : null}
                  {d.state === 'pending' ? <Clock size={13} /> : null}
                  {d.state === 'blocked' || d.state === 'permablocked' ? <AlertTriangle size={13} /> : null}
                  {info.title}
                </span>
                {left ? <span className="text-xs text-(--base-06)">{left}</span> : null}
              </div>
              <p className="mt-1 text-xs text-(--base-06)">{info.body}</p>

              {d.state === 'permablocked' && !d.txtValue ? (
                <button
                  type="button"
                  onClick={() => getToken(d.domain)}
                  disabled={busy === d.domain}
                  className="btn btn-secondary btn-sm mt-2"
                >
                  {busy === d.domain ? 'Working…' : 'Show me how to unblock it'}
                </button>
              ) : null}

              {d.txtValue ? (
                <div className="mt-2 space-y-2">
                  <div className="flex flex-col gap-[5px]">
                    <label className="input-label">TXT record name</label>
                    <div className="flex items-center gap-2">
                      <code className="input-mono flex-1 truncate rounded-md border border-(--base-03) bg-(--base-01) px-3 py-2 text-xs text-(--base-08)">
                        {d.txtName}
                      </code>
                      <button type="button" onClick={() => copy(d.txtName!)} className="btn btn-secondary btn-sm">
                        {copied === d.txtName ? <Check size={12} /> : <Copy size={12} />}
                      </button>
                    </div>
                  </div>
                  <div className="flex flex-col gap-[5px]">
                    <label className="input-label">TXT record value</label>
                    <div className="flex items-center gap-2">
                      <code className="input-mono flex-1 truncate rounded-md border border-(--base-03) bg-(--base-01) px-3 py-2 text-xs text-(--base-08)">
                        {d.txtValue}
                      </code>
                      <button type="button" onClick={() => copy(d.txtValue!)} className="btn btn-secondary btn-sm">
                        {copied === d.txtValue ? <Check size={12} /> : <Copy size={12} />}
                      </button>
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => verify(d.domain)}
                    disabled={busy === d.domain}
                    className="btn btn-primary btn-sm"
                  >
                    {busy === d.domain ? 'Checking…' : "I've added it - check now"}
                  </button>
                </div>
              ) : null}

              {note && note.domain === d.domain ? (
                <p className={`mt-2 text-xs ${note.ok ? 'text-(--success)' : 'text-(--warning)'}`}>
                  {note.text}
                </p>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
