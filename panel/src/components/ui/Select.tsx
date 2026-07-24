"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { ChevronDown, Check } from 'lucide-react';
import { selectKeyReducer, type SelectKeyState } from './selectKeyboard';

export interface SelectOption {
    value: string;
    label: string;
    badge?: string;
}

export interface SelectProps {
    value: string;
    onChange: (value: string) => void;
    options: SelectOption[];
    placeholder?: string;
    disabled?: boolean;
    className?: string;
    ariaLabel?: string;
}

// Reusable branded single-select dropdown. Mirrors NotificationsDropdown's
// open-state + wrapRef + click-outside pattern; the trigger reuses the
// `.input-field` class so it matches every other input pixel-for-pixel
// (including the focus ring), and the menu reuses `.dropdown-menu` /
// `.dropdown-item`. Keyboard navigation is delegated to the pure
// `selectKeyReducer`; that logic is unit-tested there since this component
// has no DOM test tooling to exercise it directly.
export default function Select({
    value,
    onChange,
    options,
    placeholder = 'Select…',
    disabled = false,
    className = '',
    ariaLabel,
}: SelectProps) {
    const [state, setState] = useState<SelectKeyState>({ open: false, highlight: -1 });
    const wrapRef = useRef<HTMLDivElement>(null);

    const selectedIndex = options.findIndex(o => o.value === value);
    const selected = selectedIndex >= 0 ? options[selectedIndex] : undefined;

    // Click-outside closes the dropdown, same pattern as NotificationsDropdown.
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (!wrapRef.current?.contains(e.target as Node)) {
                setState(s => (s.open ? { ...s, open: false } : s));
            }
        };
        document.addEventListener('click', handler);
        return () => document.removeEventListener('click', handler);
    }, []);

    const handleKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
        if (['ArrowDown', 'ArrowUp', 'Enter', ' ', 'Escape'].includes(e.key)) {
            e.preventDefault();
        }
        const r = selectKeyReducer(state, e.key, options.length, selectedIndex);
        setState(r.state);
        if (r.commit != null) onChange(options[r.commit].value);
    };

    const handleOptionClick = (index: number) => {
        onChange(options[index].value);
        setState(s => ({ ...s, open: false }));
    };

    return (
        <div ref={wrapRef} className={`relative ${className}`}>
            <button
                type="button"
                disabled={disabled}
                aria-haspopup="listbox"
                aria-expanded={state.open}
                aria-label={ariaLabel}
                onClick={() => setState(s => ({
                    open: !s.open,
                    highlight: s.open ? s.highlight : (selectedIndex >= 0 ? selectedIndex : 0),
                }))}
                onKeyDown={handleKeyDown}
                className={`input-field w-full flex items-center justify-between gap-2 text-left ${disabled ? 'opacity-40' : 'cursor-pointer'}`}
            >
                <span className={`truncate ${selected ? 'text-(--base-09)' : 'text-(--base-06)'}`}>
                    {selected ? selected.label : placeholder}
                </span>
                <ChevronDown
                    size={14}
                    className={`shrink-0 text-(--base-06) transition-transform ${state.open ? 'rotate-180' : ''}`}
                />
            </button>

            {state.open && !disabled && (
                <div role="listbox" className="dropdown-menu left-0 right-0 mt-1 max-h-60 overflow-y-auto">
                    {options.map((option, index) => {
                        const isSelected = option.value === value;
                        const isHighlighted = index === state.highlight;
                        return (
                            <button
                                key={option.value}
                                type="button"
                                role="option"
                                aria-selected={isSelected}
                                onMouseEnter={() => setState(s => ({ ...s, highlight: index }))}
                                onClick={() => handleOptionClick(index)}
                                className={`dropdown-item justify-between gap-2 ${isHighlighted ? 'bg-(--base-03) text-(--base-09)' : ''} ${isSelected ? 'text-(--accent-light)' : ''}`}
                            >
                                <span className="truncate">{option.label}</span>
                                <span className="flex items-center gap-2 shrink-0">
                                    {option.badge && <span className="mono-label">{option.badge}</span>}
                                    {isSelected && <Check size={13} className="text-(--accent-light)" />}
                                </span>
                            </button>
                        );
                    })}
                </div>
            )}
        </div>
    );
}
