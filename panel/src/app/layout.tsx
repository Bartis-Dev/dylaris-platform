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
