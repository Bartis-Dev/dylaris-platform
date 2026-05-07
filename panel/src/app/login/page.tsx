"use client";

import React, { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import LoginForm from "@/components/LoginForm";

export default function LoginPage() {
  const router = useRouter();

  useEffect(() => {
    // If user already has a token, redirect to /servers (or stored deep link)
    const token = localStorage.getItem('token') || localStorage.getItem('authToken');
    if (token) {
      const target = sessionStorage.getItem('postLoginRedirect') || '/servers';
      sessionStorage.removeItem('postLoginRedirect');
      router.push(target);
    }
  }, [router]);

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-(--background)">
      <LoginForm />
    </div>
  );
}
