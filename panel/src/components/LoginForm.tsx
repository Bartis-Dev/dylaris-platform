"use client";

import React, { useState, useRef, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { login } from '../lib/api/auth';
import { ShieldCheck, ArrowLeft } from 'lucide-react';

type Step = 'credentials' | '2fa';

export default function LoginForm() {
  const [step, setStep] = useState<Step>('credentials');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [totpCode, setTotpCode] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const router = useRouter();
  const totpRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (step === '2fa') totpRef.current?.focus();
  }, [step]);

  const finish = (token: string) => {
    void token;
    const target = sessionStorage.getItem('postLoginRedirect') || '/servers';
    sessionStorage.removeItem('postLoginRedirect');
    router.push(target);
  };

  const handleCredentialsSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const result = await login(username, password);
      if (result.success) {
        finish(result.token);
      } else if (result.requires2FA) {
        setStep('2fa');
      } else {
        setError(result.message || 'Login failed.');
      }
    } catch {
      setError('Could not connect to the server.');
    } finally {
      setLoading(false);
    }
  };

  const handle2FASubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const result = await login(username, password, totpCode.replace(/\s/g, ''));
      if (result.success) {
        finish(result.token);
      } else {
        setError(result.message || 'Invalid 2FA code.');
      }
    } catch {
      setError('Could not connect to the server.');
    } finally {
      setLoading(false);
    }
  };

  const back = () => {
    setStep('credentials');
    setTotpCode('');
    setError('');
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

      {step === 'credentials' ? (
        <form onSubmit={handleCredentialsSubmit} className="space-y-5">
          <div className="flex flex-col gap-[5px]">
            <label htmlFor="username" className="input-label">Username</label>
            <input
              type="text"
              id="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              disabled={loading}
              autoComplete="username"
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
              autoComplete="current-password"
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
      ) : (
        <form onSubmit={handle2FASubmit} className="space-y-5">
          <div className="flex items-center gap-2 text-(--accent-light) mb-1">
            <ShieldCheck size={16} />
            <span className="text-sm font-medium">Two-factor required</span>
          </div>
          <p className="text-xs text-(--base-06) -mt-2">
            Enter the 6-digit code from your authenticator app, or one of your single-use backup codes.
          </p>
          <div className="flex flex-col gap-[5px]">
            <label htmlFor="totp" className="input-label">Code</label>
            <input
              ref={totpRef}
              type="text"
              id="totp"
              value={totpCode}
              onChange={(e) => setTotpCode(e.target.value)}
              disabled={loading}
              autoComplete="one-time-code"
              inputMode="text"
              placeholder="123 456"
              className="input-field input-mono w-full text-center tracking-widest text-lg disabled:opacity-40"
            />
          </div>
          <button
            type="submit"
            disabled={loading || totpCode.trim().length < 6}
            className="btn btn-primary w-full py-3 text-sm"
          >
            {loading ? 'Verifying...' : 'Verify & Login'}
          </button>
          <button
            type="button"
            onClick={back}
            disabled={loading}
            className="flex items-center justify-center gap-1.5 w-full text-xs text-(--base-06) hover:text-(--base-08) transition-colors"
          >
            <ArrowLeft size={12} />
            Back to login
          </button>
        </form>
      )}
    </div>
  );
}
