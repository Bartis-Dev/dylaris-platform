"use client";

import SetupWizard from '@/components/setup/Wizard';

export default function SetupPage() {
    return (
        <div className="min-h-screen flex items-center justify-center p-4 bg-(--background)">
            <SetupWizard />
        </div>
    );
}
