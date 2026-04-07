"use client";

import React, { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
// import directly from the new module path
import { getProfile } from '../lib/api'; 
import Dashboard from '@/components/Dashboard';

export default function Home() {
  const [isAuthenticated, setIsAuthenticated] = useState<boolean | null>(null);
  const router = useRouter();

  useEffect(() => {
    const verifyToken = async () => {
      // FIX: Look for the new key "token" instead of "authToken"
      const token = localStorage.getItem("token");

      if (!token) {
        // Silently redirect to login, without console errors
        router.push('/login');
        return;
      }

      try {
        const profile = await getProfile();

        if (profile) {
          setIsAuthenticated(true);
        } else {
          // Token invalid or expired -> remove and redirect to login
          localStorage.removeItem("token"); 
          router.push('/login');
        }
      } catch (error) {
        // Critical error during API call -> safety logout
        localStorage.removeItem("token"); 
        router.push('/login');
      }
    };

    verifyToken();
  }, [router]);

  if (isAuthenticated === null) {
    return (
      <div className="flex h-screen items-center justify-center bg-(--background) text-2xl text-(--foreground-secondary)">
        Checking authentication...
      </div>
    );
  }

  if (isAuthenticated) {
    return <Dashboard />;
  }

  return null;
}