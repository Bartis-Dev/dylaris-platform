"use client";

import React, { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import LoginForm from "@/components/LoginForm";
import { getSetupStatus } from '@/lib/api/setup';

export default function LoginPage() {
  const router = useRouter();

  useEffect(() => {
    (async () => {
      // Phase 17 — Fresh-Install means no users exist at all, so login
      // would 503. Redirect to /setup. Lost-Admin mode still lets
      // non-admin users log in normally — no redirect for that case.
      const status = await getSetupStatus();
      if (status.mode === 'fresh_install') {
        router.replace('/setup');
        return;
      }
      const token = localStorage.getItem('token') || localStorage.getItem('authToken');
      if (token) {
        const target = sessionStorage.getItem('postLoginRedirect') || '/servers';
        sessionStorage.removeItem('postLoginRedirect');
        router.push(target);
      }
    })();
  }, [router]);

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-(--background)">
      <LoginForm />
    </div>
  );
}
