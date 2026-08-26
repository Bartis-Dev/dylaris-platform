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
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import { toast } from '@/components/ui/Toast';

interface ModulesTabProps {
    modules: AppModule[];
    onModulesChange: () => void;
}

// Built-in modules cannot be deleted (kept in sync with Core's seed list +
// builtInModules check in handlers/modules.go). Gateway was retired and its
// content moved into Infrastructure's Routes tab. Tickets is included here
// even though it is_system=false server-side (it stays toggle-able) - only
// deletion is blocked, both here (hides the button) and in Core (rejects the
// request), same as Library.
const BUILTIN_MODULES = new Set(['Servers', 'Admin', 'Infrastructure', 'Library', 'Tickets', 'Modpacks']);

// Modules whose enabled state AND audience are DERIVED from feature flags rather
// than set here. Offering the controls would be worse than hiding them: an edit
// would appear to work and then be silently undone the next time the owning
// flags are saved. Position stays editable - reordering is this screen's job.
//
// Modpacks follows Settings -> Features: it appears with the Modpacks subsystem
// and widens from admin-only to everyone with "Open authoring to users".
const DERIVED_MODULES = new Map([
    ['Modpacks', 'Settings -> Features -> Modpacks'],
]);

interface SortableModuleCardProps {
    module: AppModule;
    onToggle: (id: number, current: boolean) => void;
    onDelete: (id: number) => void;
    onRoleChange: (id: number, role: 'all' | 'admin') => void;
}

function SortableModuleCard({ module: m, onToggle, onDelete, onRoleChange }: SortableModuleCardProps) {
    const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: m.id });
    // Non-empty when this row's state comes from feature flags: names where the
    // admin can change it, so the lock reads as a pointer rather than a dead end.
    const derivedFrom = DERIVED_MODULES.get(m.name);

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
                {/* Role toggle - Servers is hard-locked to "all", Admin and
                    Infrastructure to "admin" (Infrastructure's own page
                    hard-gates isAdmin, so the "all" option would never take
                    effect anyway - the toggle is locked to avoid a misleading
                    control) */}
                {derivedFrom ? (
                    <div
                        className="inline-flex items-center gap-1 mono-label px-2 py-1 rounded-md bg-(--base-03)"
                        title={`Audience follows ${derivedFrom}`}
                    >
                        {m.accessRole === 'all' ? <><Users size={11} /> All</> : <><ShieldCheck size={11} /> Admin</>}
                    </div>
                ) : m.name === 'Servers' ? (
                    <div className="inline-flex items-center gap-1 mono-label px-2 py-1 rounded-md bg-(--base-03)" title="Always visible to all users">
                        <Users size={11} /> All
                    </div>
                ) : m.name === 'Admin' || m.name === 'Infrastructure' ? (
                    <div className="inline-flex items-center gap-1 mono-label px-2 py-1 rounded-md bg-(--base-03)" title="Always admin-only">
                        <ShieldCheck size={11} /> Admin
                    </div>
                ) : (
                    <div
                        className="flex bg-(--base-03) p-0.5 rounded-md"
                        title={m.name === 'Library' ? "All: Library becomes a browsable/downloadable tab for users (upload/delete stay admin-only). Admin: hidden from users." : "Who can see this module"}
                    >
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

                {derivedFrom ? (
                    <div
                        className={`${m.isEnabled ? 'toggle-track toggle-track-on' : 'toggle-track toggle-track-off'} opacity-50 cursor-not-allowed`}
                        title={`Enabled state follows ${derivedFrom}`}
                    >
                        <span className={m.isEnabled ? 'toggle-knob toggle-knob-on' : 'toggle-knob toggle-knob-off'} />
                    </div>
                ) : m.name === 'Servers' || m.name === 'Admin' ? (
                    <div className="toggle-track toggle-track-on opacity-50 cursor-not-allowed" title={`${m.name} module cannot be disabled`}>
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
    // Separate from `error`, which belongs to the add-module modal. These are
    // failures of the inline row actions, which used to go to window.alert.
    const [actionError, setActionError] = useState("");
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
        const previous = sortedModules;
        setSortedModules(reordered);

        // Compute new position: slot between neighbours (spaced by 10)
        const newPos = (newIndex + 1) * 10;
        const writes = [await updateModulePosition(Number(active.id), newPos)];

        // Persist all positions after reorder to keep them clean
        for (let i = 0; i < reordered.length; i++) {
            const m = reordered[i];
            const targetPos = (i + 1) * 10;
            if (m.position !== targetPos) {
                writes.push(await updateModulePosition(m.id, targetPos));
            }
        }

        // Every one of those results used to be discarded. A refused write left
        // the new order on screen and the old order in the database, so the drag
        // silently undid itself on the next load - and a partial failure left the
        // two disagreeing about only some rows. Put the list back instead.
        const failed = writes.find(r => r && r.success === false);
        if (failed) {
            setSortedModules(previous);
            setActionError(failed.message || failed.error || 'Could not save the new order.');
            return;
        }
        setActionError('');
        toast('Order saved');
        onModulesChange();
    };

    const handleCreateModule = async (e: React.FormEvent) => {
        e.preventDefault();
        const res = await createModule(modForm as any);
        if (res.success) {
            setIsModalOpen(false);
            toast('Module added');
            onModulesChange();
        } else setError(res.message);
    };

    const handleDeleteModule = async (id: number) => {
        if (!(await confirmDialog({ title: 'Delete module', message: "Do you really want to delete this module?" }))) return;
        // handleCreateModule and handleToggleModule both read res.success; this
        // one threw it away, so a refused delete just re-rendered the module
        // still sitting there with nothing said. setActionError, not setError:
        // the `error` alert only renders inside the create modal, so a card-level
        // failure written there would never be seen. handleRoleChange already
        // makes that distinction.
        const res = await deleteModule(id);
        if (!res.success) {
            setActionError(res.message || res.error || 'Could not delete the module.');
            return;
        }
        setActionError('');
        toast('Module deleted');
        onModulesChange();
    };

    const handleToggleModule = async (id: number, currentStatus: boolean) => {
        const res = await toggleModule(id, !currentStatus);
        if (res.success) {
            setActionError("");
            // These are row operations on a list, like deleting a user, so they
            // apply straight away rather than waiting for a Save. What
            // they did NOT do was say so: this screen had no toast at all, so a
            // toggle, a role change and a drag all succeeded in silence and the
            // only way to know was that something moved.
            toast(currentStatus ? 'Module disabled' : 'Module enabled');
            onModulesChange();
        } else {
            setActionError(res.message || "Could not toggle the module.");
        }
    };

    const handleRoleChange = async (id: number, role: 'all' | 'admin') => {
        const res = await setModuleAccessRole(id, role);
        if (res.success) {
            setActionError("");
            toast(role === 'admin' ? 'Module is now admin-only' : 'Module is now visible to everyone');
            onModulesChange();
        } else {
            setActionError(res.message || "Could not change the module role.");
        }
    };

    return (
        <div>
            <div className="flex justify-between items-center mb-6">
                <p className="text-sm text-(--base-07)">Manage system features and external links. Drag to reorder. Disabled modules are hidden from the sidebar.</p>
                <button onClick={() => { setModForm({ name: "", type: "iframe", icon: "link", url: "" }); setError(""); setIsModalOpen(true); }} className="btn btn-primary">
                    <Plus size={14} />
                    Add Module
                </button>
            </div>

            {actionError && <p role="alert" className="text-sm text-(--error) mb-4">{actionError}</p>}

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
                            {error && <div className="alert alert-error mb-4 font-medium">{error}</div>}

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
                                    <button type="button" onClick={() => setIsModalOpen(false)} className="btn btn-secondary">Cancel</button>
                                    <button type="submit" className="btn btn-primary">Save Module</button>
                                </div>
                            </form>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}
