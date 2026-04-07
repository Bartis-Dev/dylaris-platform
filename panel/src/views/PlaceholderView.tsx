"use client";

import React from 'react';

interface PlaceholderViewProps {
  viewName: string;
}

const PlaceholderView: React.FC<PlaceholderViewProps> = ({ viewName }) => {
  return (
    <div className="card p-6">
      <h2 className="modal-title text-(--accent-light) mb-4">
        {viewName.charAt(0).toUpperCase() + viewName.slice(1)} View
      </h2>
      <p className="text-sm text-(--base-07)">Content for the {viewName} view will be added here soon.</p>
    </div>
  );
};

export default PlaceholderView;
