"use client";

import { useAppData } from '@/lib/AppDataContext';
import PlaceholderView from '@/views/PlaceholderView';
import { useRouteId } from '@/lib/routeParams';

export default function CustomModulePage() {
    const paramId = useRouteId('modules');
    const { modules } = useAppData();
    const m = modules.find(x => String(x.id) === String(paramId));

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            {!m ? (
                <div className="text-center text-(--base-06)">Module not found.</div>
            ) : m.type === 'iframe' && m.url ? (
                <div className="w-full h-full rounded-xl overflow-hidden border border-(--base-03)">
                    <iframe src={m.url} className="w-full h-full" title={m.name} />
                </div>
            ) : (
                <PlaceholderView viewName={m.name} />
            )}
        </main>
    );
}
