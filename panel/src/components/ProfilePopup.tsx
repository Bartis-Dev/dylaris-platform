"use client";

import { useState } from 'react';
import { X } from 'lucide-react';

interface UserProfile {
    username: string;
    minecraftUsername?: string;
    email?: string;
    is2FAEnabled?: boolean;
}

interface ProfilePopupProps {
  currentUser: UserProfile;
  onClose: () => void;
  onUpdate: (data: {
      newUsername?: string;
      oldPassword: string;
      newPassword?: string;
      minecraftUsername?: string;
      email?: string;
      is2FAEnabled?: boolean;
  }) => Promise<void>;
  error: string;
  success: string;
}

const ProfilePopup: React.FC<ProfilePopupProps> = ({ currentUser, onClose, onUpdate, error, success }) => {
  const [currentView, setCurrentView] = useState("general");

  const [newUsername, setNewUsername] = useState(currentUser.username || "");
  const [minecraftUsername, setMinecraftUsername] = useState(currentUser.minecraftUsername || "");
  const [email, setEmail] = useState(currentUser.email || "");

  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [is2FA, setIs2FA] = useState(currentUser.is2FAEnabled || false);

  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
      e.preventDefault();
      setLoading(true);
      await onUpdate({
          newUsername,
          oldPassword,
          newPassword: newPassword === confirmPassword ? newPassword : "",
          minecraftUsername,
          email,
          is2FAEnabled: is2FA
      });
      setLoading(false);
  };

  return (
    <div className="modal-overlay animate-fade-in">
      <div className="modal-panel w-full max-w-md">
        {/* Header */}
        <div className="modal-header flex justify-between items-center">
          <h2 className="modal-title">Profile Settings</h2>
          <button onClick={onClose} className="text-(--base-06) hover:text-(--error-light) transition-colors">
            <X size={20} />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex gap-1 px-6 pt-4 border-b border-(--base-03)">
          <button onClick={() => setCurrentView("general")} className={`pb-2.5 px-3 font-medium text-sm transition-colors ${currentView === "general" ? "border-b-2 border-(--accent) text-(--accent-light)" : "text-(--base-07) hover:text-(--base-09)"}`}>General</button>
          <button onClick={() => setCurrentView("security")} className={`pb-2.5 px-3 font-medium text-sm transition-colors ${currentView === "security" ? "border-b-2 border-(--accent) text-(--accent-light)" : "text-(--base-07) hover:text-(--base-09)"}`}>Security</button>
        </div>

        {/* Body */}
        <div className="modal-body">
          {error && <div className="bg-(--error-ghost) text-(--error-light) px-4 py-3 rounded-md mb-4 text-sm font-medium border border-(--error-border)">{error}</div>}
          {success && <div className="bg-(--success-ghost) text-(--success-light) px-4 py-3 rounded-md mb-4 text-sm font-medium border border-(--success-border)">{success}</div>}

          <form onSubmit={handleSubmit} className="space-y-4">
            {currentView === "general" && (
              <div className="space-y-4 animate-fade-in">
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Username</label>
                  <input type="text" value={newUsername} onChange={e => setNewUsername(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Email (Optional)</label>
                  <input type="email" value={email} onChange={e => setEmail(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Minecraft Username (For Avatar)</label>
                  <input type="text" value={minecraftUsername} onChange={e => setMinecraftUsername(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
              </div>
            )}

            {currentView === "security" && (
              <div className="space-y-4 animate-fade-in">
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">New Password</label>
                  <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" placeholder="Leave blank to keep current" />
                </div>
                {newPassword && (
                  <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Confirm Password</label>
                    <input type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                  </div>
                )}
                <div className="pt-2 flex items-center justify-between">
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Two-Factor Authentication</div>
                        <div className="text-xs text-(--base-06)">Add an extra layer of security.</div>
                    </div>
                    <button
                      type="button"
                      onClick={() => !loading && setIs2FA(!is2FA)}
                      className={`toggle-track ${is2FA ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                      <span className={`toggle-knob ${is2FA ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
              </div>
            )}

            <div className="pt-4 border-t border-(--base-03)">
              <div className="flex flex-col gap-[5px]">
                <label className="input-label">Current Password <span className="opacity-70">(required to save)</span></label>
                <input type="password" value={oldPassword} onChange={e => setOldPassword(e.target.value)} required disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
              </div>
            </div>

            <button type="submit" disabled={loading} className="btn btn-primary w-full py-3 mt-4 text-sm">
              {loading ? 'Saving...' : 'Save Changes'}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
};

export default ProfilePopup;
