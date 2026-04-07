import React from 'react';
import { AppModule } from '../lib/api';
import { DynamicIcon } from '../lib/icons';

interface NavbarProps {
  modules: AppModule[];
  activeView: string;
  onChangeView: (view: string) => void;
  children?: React.ReactNode;
}

export default function Navbar({ modules, activeView, onChangeView, children }: NavbarProps) {
  const sortedModules = [...modules].sort((a, b) => (a.position || 99) - (b.position || 99));

  return (
    <nav className="w-full bg-(--base-01) border-b border-(--base-03) flex items-center justify-between px-6 py-2.5 shrink-0 relative z-30">

      {/* 1. BRANDING — matches sidebar width (w-72 = 288px) minus navbar px-6 (24px) */}
      <div className="flex items-center justify-center w-[264px] shrink-0 border-r border-(--base-03) mr-6 pr-6">
        <div className="px-3.5 py-1 rounded-md bg-(--accent-dim) border border-(--accent-border) inline-flex items-center">
          <h1 className="text-2xl font-logo tracking-widest select-none">
            <span className="text-(--accent-light)">D</span>
            <span className="text-(--base-09)">ylaris</span>
          </h1>
        </div>
      </div>

      {/* 2. NAVIGATION MODULES */}
      <div className="flex items-center gap-1 flex-1 overflow-x-auto hide-scrollbar">
        {sortedModules.filter(m => m.isEnabled).map(module => {
          const isActive = activeView === module.name.toLowerCase() || activeView === String(module.id);
          return (
            <button
              key={module.id}
              onClick={() => onChangeView(String(module.id))}
              className={`btn text-sm px-3.5 py-1.5 ${
                isActive
                  ? 'bg-(--accent-ghost) text-(--accent-light) border border-(--accent-border)'
                  : 'bg-transparent text-(--base-07) border border-transparent hover:bg-(--base-04)/50 hover:text-(--base-09)'
              }`}
            >
              <DynamicIcon name={module.icon || 'grid-2x2'} size={18} className={`transition-colors ${isActive ? 'text-(--accent-light)' : 'text-(--base-06) group-hover:text-(--base-08)'}`} />
              <span className="tracking-wide">{module.name}</span>
            </button>
          );
        })}
      </div>

      {/* 3. RIGHT ACTIONS */}
      <div className="flex items-center gap-3 shrink-0 pl-4">
         {children}
      </div>
    </nav>
  );
}
