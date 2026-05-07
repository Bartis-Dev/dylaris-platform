"use client";

import React, { useState } from 'react';
import { useRouter } from 'next/navigation';
import { login } from '../lib/api/auth';

export default function LoginForm() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const router = useRouter();

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const result = await login(username, password);

      if (result.success) {
        const target = sessionStorage.getItem('postLoginRedirect') || '/servers';
        sessionStorage.removeItem('postLoginRedirect');
        router.push(target);
      } else {
        setError(result.message || 'Login failed.');
        setLoading(false);
      }
    } catch (err) {
      setError('Could not connect to the server.');
      setLoading(false);
    }
  };

  return (
    <div className="card w-full max-w-md p-8">
      <h1 className="text-4xl text-center mb-8 font-logo tracking-widest select-none">
        <span className="text-(--accent-light)">D</span>
        <span className="text-(--base-09)">ylaris</span>
      </h1>

      {error && (
        <div className="bg-(--error-ghost) border border-(--error-border) text-(--error-light) px-4 py-3 rounded-md mb-4 text-center text-sm font-medium">
          {error}
        </div>
      )}

      <form onSubmit={handleLogin} className="space-y-5">
        <div className="flex flex-col gap-[5px]">
          <label htmlFor="username" className="input-label">Username</label>
          <input
            type="text"
            id="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={loading}
            className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed"
          />
        </div>
        <div className="flex flex-col gap-[5px]">
          <label htmlFor="password" className="input-label">Password</label>
          <input
            type="password"
            id="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={loading}
            className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed"
          />
        </div>
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary w-full py-3 mt-2 text-sm"
        >
          {loading ? 'Authenticating...' : 'Login'}
        </button>
      </form>
    </div>
  );
}
