// Java runtime selection for a Minecraft version.
//
// Pure logic, kept out of JavaVersionPicker.tsx so the vitest suite (src/lib
// only, no DOM stack) can cover it. The picker re-exports these so existing
// imports keep working.

export interface JavaImage {
    id: string;
    label: string;
    /** Version range shown under the label. */
    note: string;
    proxyNote: string;
}

export const JAVA_IMAGES: JavaImage[] = [
    { id: 'ghcr.io/dylaris-dev/platform-mc-java25:latest', label: 'Java 25', note: '26.1+', proxyNote: 'Newest' },
    { id: 'ghcr.io/dylaris-dev/platform-mc-java21:latest', label: 'Java 21', note: '1.20.5 - 26.0', proxyNote: 'Recommended' },
    { id: 'ghcr.io/dylaris-dev/platform-mc-java17:latest', label: 'Java 17', note: '1.18+', proxyNote: 'Minimum for Velocity' },
    { id: 'ghcr.io/dylaris-dev/platform-mc-java8:latest', label: 'Java 8', note: '1.7 - 1.16', proxyNote: 'BungeeCord only' },
];

export const JAVA_25 = JAVA_IMAGES[0].id;
export const JAVA_21 = JAVA_IMAGES[1].id;
export const JAVA_17 = JAVA_IMAGES[2].id;
export const JAVA_8 = JAVA_IMAGES[3].id;

/**
 * Returns the Java image a Minecraft version needs, or null if the string is not
 * a version we recognise.
 *
 * Two numbering schemes have to be told apart, and getting that wrong is what
 * shipped: everything up to 1.21.x is "1.MINOR.PATCH", where the leading 1 is
 * decoration and MINOR carries the meaning, while 26.x onwards is
 * "MAJOR.MINOR". The old code read parts[1] unconditionally, so "26.2" was
 * judged as minor=2 and fell through every branch to null - no recommendation at
 * all for exactly the versions the picker lists first.
 *
 * Minecraft 26.1 refuses to boot on anything below Java 25 and says so:
 * "Minecraft 26.1 and newer requires running the server with Java 25 or above."
 */
export function recommendJavaForVersion(version: string): string | null {
    const parts = version.split('.').map(Number);
    if (parts.some((n) => !Number.isFinite(n)) || parts.length === 0) return null;

    const first = parts[0];

    // New scheme: 26.1 and up need Java 25, 26.0 is still Java 21.
    if (first >= 26) {
        if (first > 26 || (parts[1] ?? 0) >= 1) return JAVA_25;
        return JAVA_21;
    }
    // Anything between the two schemes is not a real Minecraft version.
    if (first !== 1) return null;

    const minor = parts[1] ?? 0;
    const patch = parts[2] ?? 0;
    if (minor >= 21 || (minor === 20 && patch >= 5)) return JAVA_21;
    if (minor >= 18) return JAVA_17;
    // 1.7, not 1.8: the version picker offers 1.7 and 1.7.10 runs on Java 8, so
    // starting the range at 1.8 left the oldest selectable version with no
    // recommendation - the same gap as the 26.x one, at the other end.
    if (minor >= 7) return JAVA_8;
    return null;
}

/**
 * For Paper/Vanilla the "build" is a patch version like "1.20.11" - more precise
 * than the major "1.20" and worth using for the Java recommendation. For
 * Fabric/Forge the build is a loader version ("0.15.0", "47.2.0") that does not
 * start with the major, so fall back to the major there.
 */
export function effectiveMcVersion(major: string, build: string): string {
    if (build && (build === major || build.startsWith(major + '.'))) return build;
    return major;
}
