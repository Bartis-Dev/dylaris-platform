'use client';

import { useEffect, useState } from 'react';

// Returns a `now` epoch-ms that ticks every intervalMs so time-derived UI (the
// connectivity escalation) advances between SSE events. Default 30s: the tiers
// are 60s/5min, so 30s granularity is enough and cheap.
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
