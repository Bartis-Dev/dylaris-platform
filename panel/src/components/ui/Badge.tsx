import React from 'react';

export type BadgeVariant = 'neutral' | 'success' | 'warning' | 'accent';

const variantClasses: Record<BadgeVariant, string> = {
    neutral: 'bg-(--base-03) text-(--base-06)',
    success: 'bg-(--success-ghost) text-(--success-light)',
    warning: 'bg-(--warning-ghost) text-(--warning-light)',
    accent: 'bg-(--accent-ghost) text-(--accent-light)',
};

// Badge is the one shared pill for status/type labels across the modpack UI.
// It standardizes padding, the mono-label type style, and the color variants
// that were previously hand-rolled in four different ways.
export function Badge({
    variant = 'neutral',
    icon,
    children,
    className = '',
}: {
    variant?: BadgeVariant;
    icon?: React.ReactNode;
    children: React.ReactNode;
    className?: string;
}) {
    return (
        <span className={`mono-label inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm ${variantClasses[variant]} ${className}`}>
            {icon}
            {children}
        </span>
    );
}
