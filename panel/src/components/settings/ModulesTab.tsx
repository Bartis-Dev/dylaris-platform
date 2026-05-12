"use client";

import React, { useState, useEffect } from 'react';
import { createModule, deleteModule, toggleModule, updateModulePosition, setModuleAccessRole, AppModule } from '@/lib/api';
import { Plus, Trash2, GripVertical, ShieldCheck, Users } from 'lucide-react';
import { DynamicIcon } from '@/lib/icons';
import {
    DndContext,
    DragEndEvent,
    PointerSensor,
    useSensor,
    useSensors,
    closestCenter,
} from '@dnd-kit/core';
import {
    SortableContext,
    useSortable,
    verticalListSortingStrategy,
    arrayMove,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';

interface ModulesTabProps {
    modules: AppModule[];
    onModulesChange: () => void;
}

// Built-in modules cannot be deleted (kept in sync with Core's seed list +
// builtInModules check in handlers/modules.go).
const BUILTIN_MODULES = new Set(['Servers', 'Admin', 'Infrastructure', 'Gateway', 'Library']);

interface SortableModuleCardProps {
    module: AppModule;
    onToggle: (id: number, current: boolean) => void;
    onDelete: (id: number) => void;
    onRoleChange: (id: number, role: 'all' | 'admin') => void;
}

function SortableModuleCard({ module: m, onToggle, onDelete, onRoleChange }: SortableModuleCardProps) {
    const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: m.id });

    const style = {
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.5 : 1,
        zIndex: isDragging ? 10 : undefined,
    };

    return (
        <div
            ref={setNodeRef}
            style={style}
            className={`card p-4 flex justify-between items-center transition-colors ${m.isEnabled ? 'border-(--accent-border)' : 'opacity-60'}`}
        >
            <div className="flex items-center gap-3">
                <button
                    {...attributes}
                    {...listeners}
                    className="text-(--base-04) hover:text-(--base-07) cursor-grab active:cursor-grabbing p-0.5 touch-none"
                    title="Drag to reorder"
                    tabIndex={-1}
                >
                    <GripVertical size={16} />
                </button>
                <div className={`w-10 h-10 rounded-md flex items-center justify-center ${m.isEnabled ? 'bg-(--accent-ghost) text-(--accent-light)' : 'bg-(--base-03) text-(--base-06)'}`}>
                    <DynamicIcon name={m.icon} size={24} />
                </div>
                <div>
                    <div className="font-medium text-sm text-(--base-09) flex items-center gap-2">
                        {m.name}
                        {m.isSystem && <span className="badge badge-neutral">System</span>}
                    </div>
                    <div className="input-label mt-0.5">{m.type}</div>
                </div>
            </div>
            <div className="flex items-center gap-2">
                {/* Role toggle — Servers is hard-locked to "all" */}
                {m.name === 'Servers' ? (
                    <div className="inline-flex items-center gap-1 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06) px-2 py-1 rounded-md bg-(--base-03)" title="Always visible to all users">
                        <Users size={11} /> All
                    </div>
                ) : (
                    <div className="flex bg-(--base-03) p-0.5 rounded-md" title="Who can see this module">
                        <button
                            type="button"
                            onClick={() => onRoleChange(m.id, 'all')}
                            className={`px-2 py-1 text-[10px] rounded-sm transition-colors inline-flex items-center gap-1 ${m.accessRole === 'all' ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'}`}
                        >
                            <Users size={10} /> All
                        </button>
                        <button
                            type="button"
                            onClick={() => onRoleChange(m.id, 'admin')}
                            className={`px-2 py-1 text-[10px] rounded-sm transition-colors inline-flex items-center gap-1 ${m.accessRole === 'admin' ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'}`}
                        >
                            <ShieldCheck size={10} /> Admin
                        </button>
                    </div>
                )}

                {m.name === 'Servers' ? (
                    <div className="toggle-track toggle-track-on opacity-50 cursor-not-allowed" title="Servers module cannot be disabled">
                        <span className="toggle-knob toggle-knob-on" />
                    </div>
                ) : (
                    <button
                        onClick={() => onToggle(m.id, m.isEnabled)}
                        className={m.isEnabled ? 'toggle-track toggle-track-on' : 'toggle-track toggle-track-off'}
                        title={m.isEnabled ? "Disable" : "Enable"}
                    >
                        <span className={m.isEnabled ? 'toggle-knob toggle-knob-on' : 'toggle-knob toggle-knob-off'} />
                    </button>
                )}

                {!m.isSystem && !BUILTIN_MODULES.has(m.name) && (
                    <button onClick={() => onDelete(m.id)} className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors" title="Delete">
                        <Trash2 size={18} />
                    </button>
                )}
            </div>
        </div>
    );
}

export default function ModulesTab({ modules, onModulesChange }: ModulesTabProps) {
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [error, setError] = useState("");
    const [modForm, setModForm] = useState({ name: "", type: "iframe", icon: "link", url: "" });
    const [sortedModules, setSortedModules] = useState<AppModule[]>([]);

    useEffect(() => {
        setSortedModules([...modules].sort((a, b) => a.position - b.position));
    }, [modules]);

    const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

    const handleDragEnd = async (event: DragEndEvent) => {
        const { active, over } = event;
        if (!over || active.id === over.id) return;

        const oldIndex = sortedModules.findIndex(m => m.id === active.id);
        const newIndex = sortedModules.findIndex(m => m.id === over.id);
        const reordered = arrayMove(sortedModules, oldIndex, newIndex);

        // Optimistic update
        setSortedModules(reordered);

        // Compute new position: slot between neighbours (spaced by 10)
        const newPos = (newIndex + 1) * 10;
        await updateModulePosition(Number(active.id), newPos);

        // Persist all positions after reorder to keep them clean
        for (let i = 0; i < reordered.length; i++) {
            const m = reordered[i];
            const targetPos = (i + 1) * 10;
            if (m.position !== targetPos) {
                await updateModulePosition(m.id, targetPos);
            }
        }

        onModulesChange();
    };

    const handleCreateModule = async (e: React.FormEvent) => {
        e.preventDefault();
        const res = await createModule(modForm as any);
        if (res.success) {
            setIsModalOpen(false);
            onModulesChange();
        } else setError(res.message);
    };

    const handleDeleteModule = async (id: number) => {
        if (!confirm("Do you really want to delete this module?")) return;
        await deleteModule(id);
        onModulesChange();
    };

    const handleToggleModule = async (id: number, currentStatus: boolean) => {
        const res = await toggleModule(id, !currentStatus);
        if (res.success) {
            onModulesChange();
        } else {
            alert("Error toggling module.");
        }
    };

    const handleRoleChange = async (id: number, role: 'all' | 'admin') => {
        const res = await setModuleAccessRole(id, role);
        if (res.success) {
            onModulesChange();
        } else {
            alert(res.message || "Error changing role.");
        }
    };

    return (
        <div>
            <div className="flex justify-between items-center mb-6">
                <p className="text-sm text-(--base-07)">Manage system features and external links. Drag to reorder. Disabled modules are hidden from the sidebar.</p>
                <button onClick={() => { setModForm({ name: "", type: "iframe", icon: "link", url: "" }); setError(""); setIsModalOpen(true); }} className="btn btn-primary px-4 py-2 text-sm">
                    <Plus size={14} />
                    Add Module
                </button>
            </div>

            <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
                <SortableContext items={sortedModules.map(m => m.id)} strategy={verticalListSortingStrategy}>
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                        {sortedModules.map(m => (
                            <SortableModuleCard
                                key={m.id}
                                module={m}
                                onToggle={handleToggleModule}
                                onDelete={handleDeleteModule}
                                onRoleChange={handleRoleChange}
                            />
                        ))}
                    </div>
                </SortableContext>
            </DndContext>

            {isModalOpen && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-md">
                        <div className="modal-header">
                            <h3 className="modal-title">New Module</h3>
                        </div>
                        <div className="modal-body">
                            {error && <div className="bg-(--error-ghost) border border-(--error-border) text-(--error-light) px-3 py-2 rounded-md mb-4 text-sm font-medium">{error}</div>}

                            <form onSubmit={handleCreateModule} className="space-y-4">
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Name</label>
                                    <input required type="text" value={modForm.name} onChange={e => setModForm({ ...modForm, name: e.target.value })} className="input-field w-full" placeholder="e.g. Map" />
                                </div>
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Type</label>
                                    <select value={modForm.type} onChange={e => setModForm({ ...modForm, type: e.target.value })} className="input-field w-full">
                                        <option value="iframe">IFrame (External URL)</option>
                                        <option value="internal">Internal (Custom View)</option>
                                    </select>
                                </div>
                                {modForm.type === 'iframe' && (
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">URL</label>
                                        <input required type="url" value={modForm.url} onChange={e => setModForm({ ...modForm, url: e.target.value })} className="input-field w-full" placeholder="https://..." />
                                    </div>
                                )}
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Icon (Lucide name)</label>
                                    <div className="flex gap-2">
                                        <input required type="text" value={modForm.icon} onChange={e => setModForm({ ...modForm, icon: e.target.value })} className="input-field w-full" placeholder="e.g. map" />
                                        <div className="w-10 h-10 flex items-center justify-center bg-(--base-03) rounded-md text-(--base-07) shrink-0"><DynamicIcon name={modForm.icon || 'circle-help'} size={24} /></div>
                                    </div>
                                </div>
                                <div className="modal-footer">
                                    <button type="button" onClick={() => setIsModalOpen(false)} className="btn btn-secondary px-4 py-1.5 text-sm">Cancel</button>
                                    <button type="submit" className="btn btn-primary px-4 py-1.5 text-sm">Save Module</button>
                                </div>
                            </form>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}
