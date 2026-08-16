"use client";

import { useState } from 'react';
import { ChevronUp, ChevronDown, GripVertical, AlertTriangle } from 'lucide-react';
import { StoragePlacement as Placement } from '@/lib/api';

interface Props {
  /** Paths to order. A node's own list, or the union across the fleet. */
  paths: string[];
  placement: Placement;
  /** Persists the policy. Injected so the same editor serves one node or the fleet default. */
  save: (next: Placement) => Promise<{ success?: boolean; error?: string; placement?: Placement }>;
  onSaved: (next: Placement) => void;
  /** Copy for the auto option, which differs between a node and the fleet. */
  autoLabel?: string;
  hint?: string;
}

/**
 * effectiveOrder mirrors what the node does: configured order first, then any
 * path the order does not mention, in the node's own order. Showing the same
 * resolution the node applies is the point - otherwise the admin edits a list
 * that does not match where servers actually land.
 */
export function effectiveOrder(paths: string[], order: string[] | null | undefined): string[] {
  // Go marshals a nil slice as JSON null, and a fresh deploy has no saved
  // order - null.filter here took down the entire Placement tab. This is the
  // single choke point both the fleet and the per-node form pass through, so
  // tolerate it here rather than at every call site.
  const safe = order ?? [];
  const known = new Set(paths);
  const listed = safe.filter((p, i) => known.has(p) && safe.indexOf(p) === i);
  return [...listed, ...paths.filter((p) => !listed.includes(p))];
}

function sameOrder(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

export default function StoragePlacement({ paths, placement, save, onSaved, autoLabel = 'Most free', hint }: Props) {
  const [mode, setMode] = useState<Placement['mode']>(placement.mode);
  const [order, setOrder] = useState<string[]>(effectiveOrder(paths, placement.order));
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const dirty = mode !== placement.mode || !sameOrder(order, effectiveOrder(paths, placement.order));

  function move(from: number, to: number) {
    if (from === to || to < 0 || to >= order.length) return;
    const next = [...order];
    const [row] = next.splice(from, 1);
    next.splice(to, 0, row);
    setOrder(next);
  }

  function reset() {
    setMode(placement.mode);
    setOrder(effectiveOrder(paths, placement.order));
    setError('');
  }

  async function persist() {
    setSaving(true);
    setError('');
    try {
      // Only a manual policy carries an order; sending one for auto would store
      // a list nothing reads and make the stored config look meaningful.
      const next: Placement = { mode, order: mode === 'manual' ? order : [] };
      const res = await save(next);
      if (!res?.success) throw new Error(res?.error || 'Save failed');
      onSaved(res.placement ?? next);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  const tab = (value: Placement['mode'], label: string, hint: string) => (
    <button
      key={value}
      type="button"
      onClick={() => setMode(value)}
      aria-pressed={mode === value}
      title={hint}
      className={`flex-1 rounded-sm px-2 py-1 font-mono text-[10px] uppercase tracking-[0.08em] transition-colors
        focus:outline-none focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]
        ${mode === value
          ? 'bg-(--accent) text-(--base-09)'
          : 'bg-(--base-02) text-(--base-06) hover:text-(--base-08) hover:bg-(--base-03)'}`}
    >
      {label}
    </button>
  );

  return (
    <div className="space-y-2 border-t border-(--base-03) pt-2">
      <div className="mono-label">New servers go to</div>

      <div className="flex gap-1">
        {tab('auto', autoLabel, 'Pick whichever path has the most free space')}
        {tab('manual', 'Priority', 'Use your order; the node only skips a path that is full or unmounted')}
      </div>

      {hint && <p className="text-[10px] font-mono leading-relaxed text-(--base-05)">{hint}</p>}

      {mode === 'manual' && (
        <ul className="space-y-1">
          {order.map((p, i) => (
            <li
              key={p}
              draggable
              onDragStart={() => setDragIndex(i)}
              onDragEnd={() => setDragIndex(null)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => { if (dragIndex !== null) move(dragIndex, i); setDragIndex(null); }}
              className={`flex items-center gap-1.5 rounded-sm border border-(--base-03) bg-(--base-02) px-1.5 py-1
                ${dragIndex === i ? 'opacity-50' : ''}`}
            >
              <GripVertical size={11} className="shrink-0 cursor-grab text-(--base-05)" aria-hidden />
              <span className="w-4 shrink-0 text-center font-mono text-[10px] text-(--base-05) tabular-nums">{i + 1}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-(--base-07)">{p}</span>
              <button
                type="button"
                onClick={() => move(i, i - 1)}
                disabled={i === 0}
                aria-label={`Move ${p} up`}
                className="rounded-sm p-0.5 text-(--base-06) transition-colors hover:text-(--base-09) disabled:cursor-not-allowed disabled:opacity-30
                  focus:outline-none focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]"
              >
                <ChevronUp size={11} />
              </button>
              <button
                type="button"
                onClick={() => move(i, i + 1)}
                disabled={i === order.length - 1}
                aria-label={`Move ${p} down`}
                className="rounded-sm p-0.5 text-(--base-06) transition-colors hover:text-(--base-09) disabled:cursor-not-allowed disabled:opacity-30
                  focus:outline-none focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]"
              >
                <ChevronDown size={11} />
              </button>
            </li>
          ))}
        </ul>
      )}

      {mode === 'manual' && (
        <p className="text-[10px] font-mono leading-relaxed text-(--base-05)">
          Top path wins. A path that is full or unmounted is skipped; a disk added later joins at the bottom.
          Servers that already exist stay where they are.
        </p>
      )}

      {error && (
        <p className="flex items-start gap-1.5 rounded-sm border border-(--error-border) bg-(--error-ghost) px-2 py-1.5 text-[10px] font-mono text-(--error-light)">
          <AlertTriangle size={10} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </p>
      )}

      {dirty && (
        <div className="flex gap-1.5">
          <button
            type="button"
            onClick={persist}
            disabled={saving}
            className="flex-1 rounded-sm bg-(--accent) px-2 py-1 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-09)
              transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50
              focus:outline-none focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]"
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
          <button
            type="button"
            onClick={reset}
            disabled={saving}
            className="rounded-sm border border-(--base-03) px-2 py-1 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)
              transition-colors hover:text-(--base-08) disabled:cursor-not-allowed disabled:opacity-50
              focus:outline-none focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]"
          >
            Reset
          </button>
        </div>
      )}
    </div>
  );
}
