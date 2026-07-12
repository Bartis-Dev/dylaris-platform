// Dylaris Panel - runtime configuration.
//
// This file is served as-is and is NOT baked into the build. In the Docker
// image, panel/docker-entrypoint.sh REGENERATES this file from the PANEL_API_URL
// env var on every container start - set that env var rather than editing or
// bind-mounting this file directly, since the entrypoint overwrites it on the
// next restart. Outside Docker (e.g. `next dev` / a bare `next start`), this
// shipped default is served as-is. Build-time NEXT_PUBLIC_* variables cannot be
// changed after the image is built; this can.
//
// Leave apiUrl empty to use the same origin that serves the panel (recommended
// behind a reverse proxy that routes /api to Core). Set it to an absolute URL
// when the API lives on a different host/domain.
//
//   examples:
//     apiUrl: ""                              -> https://<panel-host>/api
//     apiUrl: "https://panel.example.com/api"
//     apiUrl: "https://api.example.com"
window.__DYLARIS_CONFIG__ = {
  apiUrl: "",
};
