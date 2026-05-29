"use client";

import React, { useEffect, useState } from 'react';
import { useRouter, usePathname } from 'next/navigation';
import { AppDataProvider, useAppData } from '@/lib/AppDataContext';
import { logout, updateProfile as apiUpdateProfile } from '@/lib/api';
import Navbar from '@/components/Navbar';
import NotificationsDropdown from '@/components/NotificationsDropdown';
import ProfilePopup from '@/components/ProfilePopup';
import MaintenanceBanner from '@/components/MaintenanceBanner';
import CoreRegionChip from '@/components/CoreRegionChip';
import GuardedLink from '@/components/GuardedLink';
import UploadManagerWidget from '@/components/UploadManagerWidget';
import { UnsavedChangesProvider } from '@/components/settings/UnsavedChanges';
import { UploadManagerProvider, UploadManagerBridge } from '@/lib/uploadManager';
import { ChevronDown, UserCog, LogOut, Wrench } from 'lucide-react';

function AuthedShell({ children }: { children: React.ReactNode }) {
    const { user, ready } = useAppData();
    const router = useRouter();
    const pathname = usePathname();

    const [isProfileDropdownOpen, setIsProfileDropdownOpen] = useState(false);
    const [showProfilePopup, setShowProfilePopup] = useState(false);
    const [popupError, setPopupError] = useState('');
    const [popupSuccess, setPopupSuccess] = useState('');

    // Click outside closes the dropdown
    useEffect(() => {
        const handleClickOutside = (e: MouseEvent) => {
            const target = e.target as HTMLElement;
            if (!target.closest('.profile-dropdown-container')) setIsProfileDropdownOpen(false);
        };
        document.addEventListener('click', handleClickOutside);
        return () => document.removeEventListener('click', handleClickOutside);
    }, []);

    const handleProfileUpdate = async (data: any) => {
        setPopupError(''); setPopupSuccess('');
        if (data.newPassword === 'passwords-do-not-match') { setPopupError('Passwords do not match.'); return; }
        const result = await apiUpdateProfile(data);
        if (result.success) {
            setPopupSuccess('Saved!');
            setTimeout(() => { setShowProfilePopup(false); setPopupSuccess(''); }, 1500);
        } else {
            setPopupError(result.message || 'An error occurred.');
        }
    };

    if (!ready || !user) {
        return (
            <div className="flex h-screen items-center justify-center bg-(--base-00) text-(--base-07)">
                Loading...
            </div>
        );
    }

    const settingsActive = pathname.startsWith('/settings');

    return (
        <div className="flex flex-col h-screen bg-(--base-00) text-(--base-09) font-body overflow-hidden">
            {/* Phase 1 — global maintenance banner. Renders nothing when off. */}
            <MaintenanceBanner />
            {/* Top Navbar */}
            <div className="relative z-30 shrink-0">
                <Navbar>
                    <CoreRegionChip />
                    <UploadManagerWidget />
                    <NotificationsDropdown />
                    {/* NotificationsDropdown self-gates: admins see both system checks and inbox;
                        regular users see only their inbox. */}
                    {user.isAdmin && (
                        <GuardedLink
                            href="/settings"
                            className={`flex items-center space-x-2 px-3 py-1.5 rounded-md transition-colors font-medium border mr-2 ${
                                settingsActive
                                    ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border)'
                                    : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent'
                            }`}
                        >
                            <Wrench size={20} />
                            <span className="text-sm hidden md:block">Settings</span>
                        </GuardedLink>
                    )}

                    {/* User Profile Dropdown */}
                    <div className="relative profile-dropdown-container border-l border-(--base-03) pl-3">
                        <button
                            onClick={() => setIsProfileDropdownOpen(!isProfileDropdownOpen)}
                            className={`flex items-center space-x-2 px-2 py-1.5 rounded-md transition-colors border border-transparent ${isProfileDropdownOpen ? 'bg-(--base-03)' : 'hover:bg-(--base-03)'}`}
                        >
                            <div className="w-8 h-8 rounded-full bg-(--accent-dim) flex items-center justify-center text-(--accent-light) font-semibold text-sm overflow-hidden border border-(--base-04)">
                                {user.minecraftUsername ? (
                                    <img src={`https://cravatar.eu/helmavatar/${user.minecraftUsername}/32.png`} alt="" className="w-full h-full object-cover" />
                                ) : (
                                    user.username.charAt(0).toUpperCase()
                                )}
                            </div>
                            <span className="font-medium hidden md:block text-sm max-w-[100px] truncate text-(--base-09)">{user.username}</span>
                            <ChevronDown size={20} className="text-(--base-06) transition-transform duration-200" style={{ transform: isProfileDropdownOpen ? 'rotate(180deg)' : 'rotate(0deg)' }} />
                        </button>

                        {isProfileDropdownOpen && (
                            <div className="dropdown-menu right-0 mt-3 w-48 animate-fade-in origin-top-right">
                                <div className="px-4 py-2 border-b border-(--base-03) mb-1">
                                    <div className="dropdown-label">Logged in as</div>
                                    <div className="font-medium truncate text-(--base-09)">{user.username}</div>
                                </div>
                                <button onClick={() => { setIsProfileDropdownOpen(false); setShowProfilePopup(true); }} className="dropdown-item">
                                    <UserCog size={20} className="mr-3" /> Edit Profile
                                </button>
                                <button onClick={() => { setIsProfileDropdownOpen(false); logout(); router.push('/login'); }} className="dropdown-item text-(--error) hover:bg-(--error-ghost) mt-1">
                                    <LogOut size={20} className="mr-3" /> Logout
                                </button>
                            </div>
                        )}
                    </div>
                </Navbar>
            </div>

            {/* Main content */}
            <div className="flex flex-1 overflow-hidden relative">
                {children}
            </div>

            {showProfilePopup && (
                <ProfilePopup
                    currentUser={user}
                    onClose={() => setShowProfilePopup(false)}
                    onUpdate={handleProfileUpdate}
                    error={popupError}
                    success={popupSuccess}
                />
            )}
        </div>
    );
}

export default function AuthedLayout({ children }: { children: React.ReactNode }) {
    const router = useRouter();
    const [tokenChecked, setTokenChecked] = useState(false);

    // Token presence check (full validation happens in AppDataProvider via getProfile)
    useEffect(() => {
        const token = localStorage.getItem('token') || localStorage.getItem('authToken');
        if (!token) {
            const target = window.location.pathname + window.location.search;
            sessionStorage.setItem('postLoginRedirect', target);
            router.push('/login');
            return;
        }
        setTokenChecked(true);
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    const handleUnauthenticated = () => {
        localStorage.removeItem('token');
        localStorage.removeItem('authToken');
        const target = window.location.pathname + window.location.search;
        sessionStorage.setItem('postLoginRedirect', target);
        router.push('/login');
    };

    if (!tokenChecked) {
        return (
            <div className="flex h-screen items-center justify-center bg-(--base-00) text-(--base-07)">
                Checking authentication...
            </div>
        );
    }

    return (
        <AppDataProvider onUnauthenticated={handleUnauthenticated}>
            <UnsavedChangesProvider>
                <UploadManagerProvider>
                    <UploadManagerBridge />
                    <AuthedShell>{children}</AuthedShell>
                </UploadManagerProvider>
            </UnsavedChangesProvider>
        </AppDataProvider>
    );
}
