import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Minimal Vite config — the Beam frontend is just the Panel-URL
// settings page, no Tailwind / no shared packages / no aliases needed.
//
// base: './' makes the built asset references relative (./assets/…).
// The page is served at /__beam/ by the Wails asset server, so a
// relative href resolves to /__beam/assets/… — which the proxy
// middleware routes back to the embedded bundle. An absolute /assets/…
// would instead get reverse-proxied to the remote Panel and 404.
export default defineConfig({
  base: './',
  plugins: [react()],
  build: { outDir: 'dist' },
});
