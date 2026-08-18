"use client";

import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { usePathname } from 'next/navigation';
import { MoreHorizontal } from 'lucide-react';
import { AppModule } from '../lib/api';
import { DynamicIcon } from '../lib/icons';
import { useAppData } from '@/lib/AppDataContext';
import { useLayout } from '@/lib/useBreakpoint';
import GuardedLink from '@/components/GuardedLink';

interface NavbarProps {
  children?: React.ReactNode;
  /** Rendered in the branding slot; collapses with the sidebar. */
  brand?: React.ReactNode;
}

// Map a DB-loaded module to its URL route.
//
// Every built-in module row is seeded WITH its route in `url` (see
// seedSystemModules), so that column is the mapping - not a name switch that
// has to be extended for each new module. It wasn't, which is why Tickets and
// Modpacks both opened /modules/<id> and rendered a "coming soon" placeholder
// while their real pages sat unreachable at /tickets and /modpacks.
//
// Only INTERNAL modules route by url; an iframe module's url is a foreign
// origin and belongs in the iframe on /modules/<id>, not in the address bar.
// The path check rejects "//host" (protocol-relative, i.e. off-site) so an
// admin-editable column can never turn a nav entry into an off-site redirect.
// Gateway was retired as a standalone module — its content moved into the
// Infrastructure module's Routes tab and any legacy DB row is filtered out
// server-side.
export function moduleHref(module: AppModule): string {
  const url = module.url?.trim();
  if (module.type === 'internal' && url && url.startsWith('/') && !url.startsWith('//')) {
    return url;
  }
  return `/modules/${module.id}`;
}

export default function Navbar({ children, brand }: NavbarProps) {
  const { modules, featureFlags } = useAppData();
  const pathname = usePathname();
  const { layout } = useLayout();

  const sortedModules = [...modules]
    // Platform-wide tickets toggle hides the Tickets module entry-point.
    // The module row itself stays in the DB so flipping the toggle back on
    // restores the nav without re-seeding.
    .filter(m => featureFlags.tickets || m.name !== 'Tickets')
    .filter(m => m.isEnabled)
    .sort((a, b) => (a.position || 99) - (b.position || 99));

  // How many modules fit. Measured rather than derived from a breakpoint,
  // because the count is admin-controlled: an install with four modules and one
  // with fourteen need different answers at the same width, and guessing from
  // the viewport gets both wrong.
  const stripRef = useRef<HTMLDivElement>(null);
  const [visibleCount, setVisibleCount] = useState(sortedModules.length);
  const [overflowOpen, setOverflowOpen] = useState(false);

  // Labels are dropped before anything is hidden: an icon-only row fits roughly
  // three times as many entries, so at most widths nothing has to overflow at
  // all. Only when even that is not enough does the overflow menu collect.
  const iconOnly = layout !== 'wide';

  useLayoutEffect(() => {
    const strip = stripRef.current;
    if (!strip) return;

    const measure = () => {
      const available = strip.clientWidth;
      const items = Array.from(strip.querySelectorAll<HTMLElement>('[data-nav-item]'));
      if (items.length === 0) return;
      // Reserve room for the overflow trigger up front rather than discovering
      // mid-loop that it no longer fits.
      const reserve = 44;
      let used = 0;
      let fits = 0;
      for (const item of items) {
        used += item.offsetWidth + 4;
        if (used > available - reserve) break;
        fits++;
      }
      setVisibleCount(prev => (prev === fits ? prev : Math.max(fits, 0)));
    };

    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(strip);
    return () => ro.disconnect();
  }, [sortedModules.length, iconOnly]);

  // Close on navigation. GuardedLink owns its own click (it may pop the
  // unsaved-changes dialog instead of navigating), so the menu cannot close
  // itself from an onClick without defeating that guard.
  useEffect(() => { setOverflowOpen(false); }, [pathname]);

  useEffect(() => {
    if (!overflowOpen) return;
    const close = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest('.nav-overflow')) setOverflowOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [overflowOpen]);

  const visible = sortedModules.slice(0, visibleCount);
  const overflow = sortedModules.slice(visibleCount);

  const itemClass = (isActive: boolean) =>
    `btn text-sm ${iconOnly ? 'px-2 py-1.5' : 'px-3.5 py-1.5'} ${
      isActive
        ? 'bg-(--accent-ghost) text-(--accent-light) border border-(--accent-border)'
        : 'bg-transparent text-(--base-07) border border-transparent hover:bg-(--base-04)/50 hover:text-(--base-09)'
    }`;

  const isActiveHref = (href: string) => pathname === href || pathname.startsWith(href + '/');

  return (
    <nav className="w-full bg-(--base-01) border-b border-(--base-03) flex items-center justify-between px-6 py-2.5 shrink-0 relative z-30">
      {/* Branding. Width is handed in by the shell so it stays aligned with the
          sidebar underneath it - the two columns only line up because they are
          the same width, and a collapsed sidebar must not leave the logo
          hanging over the content. */}
      {brand}

      {/* Navigation modules. Never scrolled out of reach: the strip used to be
          overflow-x-auto with the scrollbar hidden, so entries past the fold
          were unreachable AND invisible. */}
      <div ref={stripRef} className="flex items-center gap-1 flex-1 min-w-0 overflow-hidden">
        {visible.map(module => {
          const href = moduleHref(module);
          const isActive = isActiveHref(href);
          return (
            <GuardedLink
              key={module.id}
              href={href}
              data-nav-item
              title={iconOnly ? module.name : undefined}
              className={itemClass(isActive)}
            >
              <DynamicIcon name={module.icon || 'grid-2x2'} size={18} className={`transition-colors ${isActive ? 'text-(--accent-light)' : 'text-(--base-06) group-hover:text-(--base-08)'}`} />
              {!iconOnly && <span className="tracking-wide">{module.name}</span>}
            </GuardedLink>
          );
        })}

        {overflow.length > 0 && (
          <div className="relative nav-overflow shrink-0">
            <button
              type="button"
              onClick={() => setOverflowOpen(o => !o)}
              aria-haspopup="menu"
              aria-expanded={overflowOpen}
              title={`${overflow.length} more`}
              className={itemClass(overflow.some(m => isActiveHref(moduleHref(m))))}
            >
              <MoreHorizontal size={18} />
            </button>
            {overflowOpen && (
              <div className="dropdown-menu left-0 mt-2 w-52 animate-fade-in origin-top-left">
                {overflow.map(module => (
                  <GuardedLink
                    key={module.id}
                    href={moduleHref(module)}
                    className="dropdown-item"
                  >
                    <DynamicIcon name={module.icon || 'grid-2x2'} size={18} className="mr-3 text-(--base-06)" />
                    {module.name}
                  </GuardedLink>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Right actions. Empty in the compact band - the shell moves them into
          the sidebar rail there, where there is room. */}
      <div className="flex items-center gap-3 shrink-0 pl-4">
        {children}
      </div>
    </nav>
  );
}
