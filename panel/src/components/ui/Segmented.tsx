'use client';

/**
 * A small inline choice between a handful of mutually exclusive options.
 *
 * Not Tabs: Tabs is page-level sub-navigation and swaps what is on screen.
 * This is a form control that changes a parameter of what is already on screen,
 * so it sits next to a label like a select does and carries aria-pressed rather
 * than a tablist role.
 */
export interface SegmentedOption<Id extends string = string> {
    id: Id;
    label: string;
    /** Shown as the option's title; use it for the "what does this mean" line. */
    hint?: string;
    disabled?: boolean;
}

export default function Segmented<Id extends string = string>({
    options,
    value,
    onChange,
    ariaLabel,
    className = '',
}: {
    options: SegmentedOption<Id>[];
    value: Id;
    onChange: (id: Id) => void;
    ariaLabel: string;
    className?: string;
}) {
    return (
        <div
            role="group"
            aria-label={ariaLabel}
            className={`inline-flex items-center gap-1 p-1 rounded-md bg-(--base-02) border border-(--base-04) ${className}`}
        >
            {options.map(({ id, label, hint, disabled }) => {
                const active = id === value;
                return (
                    <button
                        key={id}
                        type="button"
                        title={hint}
                        disabled={disabled}
                        aria-pressed={active}
                        onClick={() => onChange(id)}
                        className={`rounded px-2.5 py-1 text-xs transition-colors focus-visible:outline-none focus-visible:[box-shadow:var(--focus-ring)] disabled:opacity-40 disabled:cursor-not-allowed ${
                            active
                                ? 'bg-(--accent) text-(--base-00)'
                                : 'text-(--base-07) hover:enabled:bg-(--base-03) hover:enabled:text-(--base-09)'
                        }`}
                    >
                        {label}
                    </button>
                );
            })}
        </div>
    );
}
