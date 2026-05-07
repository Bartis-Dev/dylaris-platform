"use client";

import ModulesTab from '@/components/settings/ModulesTab';
import { useAppData } from '@/lib/AppDataContext';

export default function SettingsModulesPage() {
    const { modules, refreshModules } = useAppData();
    return <ModulesTab modules={modules} onModulesChange={refreshModules} />;
}
