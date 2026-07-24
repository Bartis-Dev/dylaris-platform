// Pure helpers for the Content tab's optional "declare loader + MC version"
// flow. An imported/uploaded server arrives with a blank MinecraftVersion and
// a non-loader InstallerType (e.g. "upload"), which silently disables the
// Content tab's loader/version auto-filtering (page.tsx defaultLoader /
// defaultMcVersion). isImportedServer flags that case so the page can show an
// optional, dismissible callout recommending the declare flow - it never
// blocks manual/advanced filtering either way.

// The Modrinth loader tags the Content tab filters by. Mirrors core
// pkg/validate.IsModrinthLoader (validate.go) - kept in sync across both.
export const LOADER_OPTIONS = [
    'paper', 'spigot', 'bukkit', 'purpur',
    'fabric', 'forge', 'quilt', 'neoforge',
    'velocity', 'waterfall', 'bungeecord',
] as const;

const KNOWN_LOADERS = new Set<string>(LOADER_OPTIONS);

export function isKnownLoader(loader: string): boolean {
    return KNOWN_LOADERS.has(loader.toLowerCase());
}

// True when the server is missing metadata the Content tab's auto-filtering
// needs: a blank MinecraftVersion, and/or an InstallerType that isn't a
// Modrinth loader tag (e.g. "upload", "upload-zip", "library", or blank).
export function isImportedServer(server: { installerType?: string; minecraftVersion?: string }): boolean {
    const hasVersion = !!server.minecraftVersion?.trim();
    const hasKnownLoader = isKnownLoader(server.installerType || '');
    return !hasVersion || !hasKnownLoader;
}
