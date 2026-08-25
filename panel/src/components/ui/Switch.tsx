'use client';

import React from 'react';

/**
 * The panel's on/off switch.
 *
 * The markup below was copied by hand into 19 files - the same four lines, with
 * the on/off class pair spelled out at each site. None of them had a focus
 * ring, a hover state or a disabled treatment; one of them had a disabled
 * cursor, in a Tailwind class, at a single call site.
 *
 * Use a Switch for one thing being on or off. Use a Checkbox for a value inside
 * a form or a multi-select.
 */
export interface SwitchProps {
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
    /** Required: the switch renders no text of its own. */
    ariaLabel: string;
    className?: string;
}

export default function Switch({
    checked,
    onChange,
    disabled = false,
    ariaLabel,
    className = '',
}: SwitchProps) {
    return (
        <button
            type="button"
            role="switch"
            aria-checked={checked}
            aria-label={ariaLabel}
            disabled={disabled}
            onClick={() => onChange(!checked)}
            className={`toggle-track ${checked ? 'toggle-track-on' : 'toggle-track-off'} ${className}`}
        >
            <span className={`toggle-knob ${checked ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
        </button>
    );
}

/**
 * SwitchRow is the layout every settings page reimplements around a Switch:
 * label and description on the left, the control hard right, the whole row
 * separated from its neighbour.
 */
export function SwitchRow({
    label,
    description,
    checked,
    onChange,
    disabled = false,
    className = '',
    children,
}: {
    label: React.ReactNode;
    description?: React.ReactNode;
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
    className?: string;
    /** Rendered under the row: a warning, a dependent field, an explanation. */
    children?: React.ReactNode;
}) {
    return (
        <div className={className}>
            <div className="flex items-center justify-between gap-4">
                <div className="min-w-0">
                    <div className="text-sm font-medium text-(--base-09)">{label}</div>
                    {description && (
                        <div className="text-xs text-(--base-06) mt-0.5">{description}</div>
                    )}
                </div>
                <Switch
                    checked={checked}
                    onChange={onChange}
                    disabled={disabled}
                    ariaLabel={typeof label === 'string' ? label : 'Toggle'}
                />
            </div>
            {children}
        </div>
    );
}
