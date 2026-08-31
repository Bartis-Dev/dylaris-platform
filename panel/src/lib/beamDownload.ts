import { API_URL } from '@/lib/api';

// Platform slugs match gateway/beam/relay/binaries.go validPlatforms.
export type BeamPlatform =
    | 'windows-amd64'
    | 'linux-amd64'
    | 'linux-arm64'
    | 'darwin-amd64'
    | 'darwin-arm64';

export function detectBeamPlatform(): BeamPlatform {
    if (typeof navigator === 'undefined') return 'windows-amd64';
    const ua = navigator.userAgent;
    const nav = navigator as Navigator & {
        userAgentData?: { platform?: string; architecture?: string };
    };
    const platform = nav.userAgentData?.platform ?? '';
    const arch = nav.userAgentData?.architecture ?? '';
    const isArm = /aarch64|arm64|arm/i.test(ua + ' ' + arch);

    if (/Mac|Darwin/i.test(ua) || /macOS/i.test(platform)) {
        return isArm ? 'darwin-arm64' : 'darwin-amd64';
    }
    if (/Linux/i.test(ua)) {
        return isArm ? 'linux-arm64' : 'linux-amd64';
    }
    return 'windows-amd64';
}

// Pulls the filename out of a Content-Disposition header, with a safe fallback
// when the upstream did not set one. Windows binaries get .exe so the OS knows
// what to do with the saved blob.
export function filenameFor(platform: BeamPlatform, contentDisp: string): string {
    const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(contentDisp);
    if (m && m[1]) return decodeURIComponent(m[1]);
    return platform.startsWith('windows') ? `beam-${platform}.exe` : `beam-${platform}`;
}

/**
 * Fetches the Beam desktop app through Core and hands it to the browser.
 *
 * Fetched rather than opened in a new tab: an error here is a JSON body
 * (`{success:false, message}`), and a plain <a href> renders that as a raw JSON
 * page in a fresh tab. Reading it lets the caller show the real reason.
 *
 * No headers and no `credentials: 'include'`. This is same-origin, so the
 * cookie goes by default; asking for credentials explicitly switches the
 * browser to the stricter CORS path that Core deliberately does not allow, and
 * it then aborts with a bare "NetworkError" that hides the real one.
 *
 * Resolves an error message, or null when the download started.
 */
export async function downloadBeamApp(): Promise<string | null> {
    const platform = detectBeamPlatform();
    try {
        const res = await fetch(`${API_URL}/beam/download?platform=${platform}`);
        if (!res.ok) {
            try {
                const body = await res.json();
                if (body?.message) return body.message as string;
            } catch {
                // Not JSON - fall through to the generic HTTP message.
            }
            return `Download failed (HTTP ${res.status}).`;
        }
        const blob = await res.blob();
        const filename = filenameFor(platform, res.headers.get('Content-Disposition') || '');
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
        return null;
    } catch (e) {
        return e instanceof Error ? e.message : 'Network error.';
    }
}
