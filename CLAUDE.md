# Dylaris - Projektübersicht
Dylaris ist ein Monorepo, das aus einer serviceorientierten Golang-Backend-Architektur und mehreren Next.js Web-Frontends besteht. Es verwaltet Container-Nodes, DNS-Einträge, Hubs, Lizenzen und Gates.

## Projektstruktur
Das Repository nutzt einen Go-Workspace (`go.work`) für die Backends und separate NPM-Projekte für die Frontends.

### Backend-Services (Golang)
- `/core`: Haupt-API, Datenbank-Modelle (Postgres/Redis), Handler und zentrale Services.
- `/node`: Node-Service für die Zielserver (inkl. Docker-Management).
- `/gate`: Gate (TCP/HTTP/Minecraft-Ingress mit TLS-Handling).
- `/hub`, `/dns/backend`, `/agent`, `/link`, `/nexus`, `/license`: Weitere spezialisierte Microservices.

### Frontends (Next.js + Tailwind v4)
- `/panel`: Haupt-Webinterface (Panel für Server- & User-Management).
- `/website`: Öffentliche Landingpage.
- `/hub/web` & `/dns/web`: Spezifische Web-UIs für Hub und DNS.

## Entwicklungs-Befehle
- **Backend**: Im jeweiligen Service-Ordner `go run main.go` oder im Root `go work sync`. Tests via `go test ./...`.
- **Frontend**: Im jeweiligen Ordner (z.B. `cd panel`) mit `npm run dev` starten (Paketmanager: npm).

## Code-Regeln & Architektur

### 1. Golang (Backend)
- **Struktur**: Behalte die Trennung zwischen Handlern (`/handlers`), Business-Logik (`/services`) und Datenbank-Zugriffen (`/store` oder `/database`) strikt bei.
- **Fehlerbehandlung**: Nutze idiomatisches Go. Immer `if err != nil` prüfen. Keine Panics in der Laufzeit, Fehler immer nach oben durchreichen oder loggen.
- **Go Workspace**: Achte darauf, dass Änderungen an Modulen mit der `go.work` Struktur kompatibel bleiben.

### 2. Next.js & React (Frontend)
- **App Router**: Alle Frontends nutzen den Next.js App Router (im `/app` Verzeichnis). Nutze keine veralteten Pages-Router-Strukturen.
- **Server Components First**: Nutze standardmäßig Server Components. Verwende die Direktive `'use client'` NUR, wenn Browser-APIs, State (`useState`) oder Lifecycle-Hooks (`useEffect`) absolut notwendig sind.
- **TypeScript**: Schreibe sauberen, typsicheren Code. Definiere Interfaces für Props und API-Antworten.

### 3. Tailwind CSS v4
- Wir verwenden **Tailwind CSS v4**! 
- **Keine Config-Datei**: Generiere niemals eine `tailwind.config.js` oder `tailwind.config.ts`.
- **CSS-First**: Alle Theme-Anpassungen, Custom Colors oder Fonts passieren direkt über `@theme` Direktiven in der globalen CSS-Datei (z.B. `/app/globals.css`).
- Nutze moderne CSS-Variablen für das Styling.

### 4. Embedded Frontends (Go + `//go:embed`)
Einige Microservices betten ihr Frontend per `//go:embed` direkt in das Go-Binary ein. **Nach jeder Änderung an Frontend-Dateien eines solchen Services MUSS zwingend ein Frontend-Build ausgeführt werden**, damit die Änderungen beim nächsten Start des Go-Backends auch tatsächlich enthalten sind.

| Service | Frontend-Ordner | Build-Command | Output-Verzeichnis |
|---------|----------------|---------------|---------------------|
| **Hub** | `hub/web/` | `cd hub/web && npm run build` | `hub/cmd/standalone/dist/` |
| **Agent** | `agent/cmd/standalone/frontend/` | Kein Build nötig (einzelne HTML-Datei) | – |

- **Wichtig**: Wird der Build vergessen, enthält das Go-Binary noch den alten Frontend-Stand – Fehler werden dann fälschlicherweise im Backend gesucht.
- Neue Services mit diesem Pattern hier ergänzen.