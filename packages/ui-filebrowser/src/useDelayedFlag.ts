import { useState, useEffect } from 'react';

// useDelayedFlag mirrors `active`, but only flips on once it has stayed
// true for `delayMs`. A fast operation (folder switch that resolves in
// well under the delay) never trips it — so the "Loading…" line doesn't
// flash on every quick navigation, only on genuinely slow loads.
export function useDelayedFlag(active: boolean, delayMs: number): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!active) {
      setShown(false);
      return;
    }
    const t = setTimeout(() => setShown(true), delayMs);
    return () => clearTimeout(t);
  }, [active, delayMs]);
  return shown;
}
