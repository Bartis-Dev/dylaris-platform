import React from 'react';

export interface ToastProps {
  message: string;
  type: 'error' | 'info';
}

// Transient bottom-center toast. The slide-in/fade-out keyframes live in
// FileBrowser's inline <style> block (shared with this component).
const Toast: React.FC<ToastProps> = ({ message, type }) => (
  <div className="toast-container" style={{ left: '50%', right: 'auto', transform: 'translateX(-50%)' }}>
    <div className="toast animate-toast">
      <div className={`toast-bar ${type === 'error' ? 'bg-(--error)' : 'bg-(--accent)'}`}></div>
      <span className="text-sm text-(--base-09)">{message}</span>
    </div>
  </div>
);

export default Toast;
