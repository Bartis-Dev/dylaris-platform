"use client";

import React, { useState } from 'react';
import { createModule, deleteModule, toggleModule, AppModule } from '@/lib/api';
import { Plus, Trash2 } from 'lucide-react';
import { DynamicIcon } from '@/lib/icons';

interface ModulesTabProps {
    modules: AppModule[];
    onModulesChange: () => void;
}

export default function ModulesTab({ modules, onModulesChange }: ModulesTabProps) {
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [error, setError] = useState("");
    const [modForm, setModForm] = useState({ name: "", type: "iframe", icon: "link", url: "" });

    const handleCreateModule = async (e: React.FormEvent) => {
        e.preventDefault();
        const res = await createModule(modForm as any);
        if (res.success) {
            setIsModalOpen(false);
            onModulesChange();
        } else setError(res.message);
    };

    const handleDeleteModule = async (id: number) => {
        if(!confirm("Do you really want to delete this module?")) return;
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

    return (
        <div>
            <div className="flex justify-between items-center mb-6">
                <p className="text-sm text-(--base-07)">Manage system features and external links. Disabled modules are hidden from the sidebar.</p>
                <button onClick={() => {setModForm({ name: "", type: "iframe", icon: "link", url: "" }); setError(""); setIsModalOpen(true);}} className="btn btn-primary px-4 py-2 text-sm">
                    <Plus size={14} />
                    Add Module
                </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                {modules.map(m => (
                    <div key={m.id} className={`card p-4 flex justify-between items-center transition-colors ${m.isEnabled ? 'border-(--accent-border)' : 'opacity-60'}`}>
                        <div className="flex items-center gap-3">
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
                            {m.name === 'Servers' ? (
                                <div className="toggle-track toggle-track-on opacity-50 cursor-not-allowed" title="Default module — cannot be disabled">
                                    <span className="toggle-knob toggle-knob-on" />
                                </div>
                            ) : (
                                <button
                                    onClick={() => handleToggleModule(m.id, m.isEnabled)}
                                    className={m.isEnabled ? 'toggle-track toggle-track-on' : 'toggle-track toggle-track-off'}
                                    title={m.isEnabled ? "Disable" : "Enable"}
                                >
                                    <span className={m.isEnabled ? 'toggle-knob toggle-knob-on' : 'toggle-knob toggle-knob-off'} />
                                </button>
                            )}

                            {!m.isSystem && (
                                <button onClick={() => handleDeleteModule(m.id)} className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors" title="Delete">
                                    <Trash2 size={18} />
                                </button>
                            )}
                        </div>
                    </div>
                ))}
            </div>

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
                                    <input required type="text" value={modForm.name} onChange={e => setModForm({...modForm, name: e.target.value})} className="input-field w-full" placeholder="e.g. Map" />
                                </div>
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Type</label>
                                    <select value={modForm.type} onChange={e => setModForm({...modForm, type: e.target.value})} className="input-field w-full">
                                        <option value="iframe">IFrame (External URL)</option>
                                        <option value="internal">Internal (Custom View)</option>
                                    </select>
                                </div>
                                {modForm.type === 'iframe' && (
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">URL</label>
                                        <input required type="url" value={modForm.url} onChange={e => setModForm({...modForm, url: e.target.value})} className="input-field w-full" placeholder="https://..." />
                                    </div>
                                )}
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Icon (Lucide name)</label>
                                    <div className="flex gap-2">
                                        <input required type="text" value={modForm.icon} onChange={e => setModForm({...modForm, icon: e.target.value})} className="input-field w-full" placeholder="e.g. map" />
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
