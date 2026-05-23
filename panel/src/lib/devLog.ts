"use client";

import { useEffect, useState } from 'react';

// Dev / Debug logger — an in-memory ring buffer plus subscription API.
// Designed for admin-only diagnostic windows: when devMode is off, all
// devLog() calls are still cheap appends to an unread buffer, but no UI
// renders so there's no observable cost.

export type DevLogLevel = 'info' | 'warn' | 'error';

export interface DevLogEntry {
    id: number;
    timestamp: number;
    category: string;
    level: DevLogLevel;
    message: string;
    data?: unknown;
}

const MAX_ENTRIES = 300;
let entries: DevLogEntry[] = [];
let nextId = 1;
const listeners = new Set<(snapshot: DevLogEntry[]) => void>();

function notify(): void {
    // Hand listeners a fresh array so React's setState identity check
    // detects the change.
    const snapshot = entries.slice();
    listeners.forEach(l => l(snapshot));
}

export function devLog(
    category: string,
    level: DevLogLevel,
    message: string,
    data?: unknown,
): void {
    const entry: DevLogEntry = {
        id: nextId++,
        timestamp: Date.now(),
        category,
        level,
        message,
        data,
    };
    entries.push(entry);
    if (entries.length > MAX_ENTRIES) {
        entries = entries.slice(entries.length - MAX_ENTRIES);
    }
    notify();
}

export function subscribeDevLog(fn: (snapshot: DevLogEntry[]) => void): () => void {
    listeners.add(fn);
    fn(entries.slice());
    return () => {
        listeners.delete(fn);
    };
}

export function clearDevLog(): void {
    entries = [];
    notify();
}

// ─────────────────────────────────────────────
// Dev mode toggle (persisted in localStorage)
// ─────────────────────────────────────────────

const DEV_MODE_KEY = 'dylaris:devMode';
const DEV_MODE_EVENT = 'dylaris:devmode-changed';

export function isDevModeEnabled(): boolean {
    if (typeof window === 'undefined') return false;
    return localStorage.getItem(DEV_MODE_KEY) === '1';
}

export function setDevModeEnabled(on: boolean): void {
    if (typeof window === 'undefined') return;
    if (on) localStorage.setItem(DEV_MODE_KEY, '1');
    else localStorage.removeItem(DEV_MODE_KEY);
    window.dispatchEvent(new Event(DEV_MODE_EVENT));
}

// React hook — re-renders consumers when devMode flips on/off, including
// across tabs (storage event) and within the same tab (custom event).
export function useDevMode(): boolean {
    const [on, setOn] = useState<boolean>(() => isDevModeEnabled());
    useEffect(() => {
        const handler = () => setOn(isDevModeEnabled());
        window.addEventListener(DEV_MODE_EVENT, handler);
        window.addEventListener('storage', handler);
        return () => {
            window.removeEventListener(DEV_MODE_EVENT, handler);
            window.removeEventListener('storage', handler);
        };
    }, []);
    return on;
}
