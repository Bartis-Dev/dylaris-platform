// Dylaris Panel - runtime configuration.
//
// This file is served as-is and is NOT baked into the build, so you can edit it
// (or bind-mount / override it in Docker) to point the panel at your API
// WITHOUT rebuilding the image. Build-time NEXT_PUBLIC_* variables cannot be
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
