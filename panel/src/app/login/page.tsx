"use client";

import React, { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import LoginForm from "@/components/LoginForm";

export default function LoginPage() {
  const router = useRouter();

  useEffect(() => {
    // FIX 1: Now looking for "token" instead of "authToken"
    const token = localStorage.getItem("token");
    if (token) {
      console.log("Token already present, attempting to redirect to main page...");
      router.push('/');
    }

    // FIX 2: The old setup logic (getAuthStatus) was completely removed,
    // since the backend configures itself via .env.
  }, [router]);

  const handleLoginSuccess = () => {
    console.log("%c1. Login successful!", "color: green; font-weight: bold;");

    // FIX 3: Also check for "token" here
    const token = localStorage.getItem("token");
    console.log("   -> Stored token in localStorage:", token);

    if (token) {
      console.log("%c2. Redirecting to main page ('/') now...", "color: blue;");
      router.push('/');
    } else {
      console.error("%cERROR: Login was successful, but no token was found in localStorage!", "color: red; font-weight: bold;");
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-(--background)">
      <LoginForm onLoginSuccess={handleLoginSuccess} />
    </div>
  );
}