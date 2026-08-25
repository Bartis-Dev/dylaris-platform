'use client';

import React from 'react';

/**
 * The panel's sub-navigation.
 *
 * Two screens used to render a SECOND vertical sidebar beside the settings
 * sidebar, and several more stacked five or six unrelated sections down one
 * scroll. Navigation depth here is capped at the sidebar plus one of these
 * bars; anything that would need a third level is a sign the page is two pages.
 */
export interface TabItem<Id extends string = string> {
    id: Id;
    label: string;
    icon?: React.ElementType;
    disabled?: boolean;
}

export default function Tabs<Id extends string = string>({
    items,
    active,
    onChange,
    className = '',
    ariaLabel = 'Sections',
}: {
    items: TabItem<Id>[];
    active: Id;
    onChange: (id: Id) => void;
    className?: string;
    ariaLabel?: string;
}) {
    return (
        <div className={`tab-bar ${className}`} role="tablist" aria-label={ariaLabel}>
            {items.map(({ id, label, icon: Icon, disabled }) => {
                const isActive = id === active;
                return (
                    <button
                        key={id}
                        type="button"
                        role="tab"
                        aria-selected={isActive}
                        disabled={disabled}
                        onClick={() => onChange(id)}
                        className={`tab ${isActive ? 'tab-active' : ''}`}
                    >
                        {Icon && (
                            <Icon
                                size={14}
                                className={isActive ? 'text-(--accent-light)' : 'text-(--base-06)'}
                            />
                        )}
                        {label}
                    </button>
                );
            })}
        </div>
    );
}
