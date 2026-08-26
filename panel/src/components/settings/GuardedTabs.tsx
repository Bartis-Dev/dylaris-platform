'use client';

import { useState } from 'react';
import Tabs, { type TabItem } from '@/components/ui/Tabs';
import { useUnsavedChangesState, UnsavedDialog } from '@/components/settings/UnsavedChanges';

/**
 * A tab bar that will not throw away unsaved work.
 *
 * A tab's sections unmount when you leave it, and unmounting unregisters them -
 * so an edit made on one tab vanished the moment another was clicked, taking the
 * save bar with it. Silently, which is the specific thing this whole arc is
 * about. Navigating AWAY from the page already prompted; moving inside it did
 * not, which is the more likely of the two.
 *
 * The same guard the Gateway page had hand-rolled around its own sub-navigation,
 * in one place so the three tabbed pages cannot drift apart.
 */
export default function GuardedTabs<Id extends string>({
    items,
    active,
    onChange,
    ariaLabel,
    className,
}: {
    items: TabItem<Id>[];
    active: Id;
    onChange: (id: Id) => void;
    ariaLabel?: string;
    className?: string;
}) {
    const registration = useUnsavedChangesState();
    const [pending, setPending] = useState<Id | null>(null);
    const [saving, setSaving] = useState(false);

    const request = (id: Id) => {
        if (id === active) return;
        if (registration?.dirty) {
            setPending(id);
            return;
        }
        onChange(id);
    };

    const close = () => {
        setPending(null);
        setSaving(false);
    };

    return (
        <>
            <Tabs items={items} active={active} onChange={request} ariaLabel={ariaLabel} className={className} />
            {pending !== null && registration && (
                <UnsavedDialog
                    saving={saving}
                    onCancel={close}
                    onSave={async () => {
                        setSaving(true);
                        let ok = false;
                        try {
                            ok = await registration.save();
                        } catch {
                            ok = false;
                        } finally {
                            setSaving(false);
                        }
                        // Only leave the tab if the work is actually stored.
                        // Switching after a refused save unmounts the section
                        // and takes the edits with it - which is the thing this
                        // component exists to prevent.
                        if (!ok) return;
                        const next = pending;
                        close();
                        onChange(next);
                    }}
                    onDiscard={() => {
                        registration.discard();
                        const next = pending;
                        close();
                        onChange(next);
                    }}
                />
            )}
        </>
    );
}
