// ci: trigger full pipeline run (no-op, safe to remove)
import type { Metadata } from "next";
import Script from "next/script";
import "./globals.css";

import { Syne, Instrument_Sans, DM_Mono, VT323 } from "next/font/google";

const syne = Syne({
  subsets: ["latin"],
  weight: ["700", "800"],
  variable: "--font-display",
});

const instrumentSans = Instrument_Sans({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-body",
});

const dmMono = DM_Mono({
  subsets: ["latin"],
  weight: ["300", "400", "500"],
  variable: "--font-mono",
});

const vt323 = VT323({
  weight: "400",
  subsets: ["latin"],
  variable: "--font-logo",
});

export const metadata: Metadata = {
  title: "Dylaris",
  description: "Dylaris MC Webinterface",
};

// Nonce-based CSP (proxy.ts) requires per-request rendering: statically
// prerendered pages have no request nonce, so their inline hydration scripts
// ship un-nonced and the browser blocks them under the strict script-src.
// Forcing dynamic rendering here cascades to every route under this layout,
// so Next stamps the per-request nonce onto every page's inline scripts.
export const dynamic = "force-dynamic";

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${syne.variable} ${instrumentSans.variable} ${dmMono.variable} ${vt323.variable}`}>
      {/* Runtime config shim (public/config.js). beforeInteractive guarantees
          window.__DYLARIS_CONFIG__ is set before the app bundle resolves the
          API URL, so self-hosters can override it without a rebuild. */}
      <Script src="/config.js" strategy="beforeInteractive" />
      <body>{children}</body>
    </html>
  );
}
