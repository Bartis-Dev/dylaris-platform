// Server-software classification helpers.
//
// A "plugin loader" runs the Bukkit/Spigot plugin API (Paper, Spigot, Purpur)
// or a proxy (Velocity, Waterfall, BungeeCord). On those, a console `reload`
// re-reads plugins/config and is a routine operation. Everything else
// (modloaders like Fabric/Forge/NeoForge/Quilt, modpacks, raw uploads, or an
// unknown installer type) treats `reload` as unstable, so callers must warn
// before firing it. Anything not positively identified as a plugin loader is
// deliberately classified as non-plugin, so the safe (warn-first) path is the
// default. Mirrors the plugin set in the Content tab's PROJECT_TYPE_FOR_LOADER.

const PLUGIN_LOADERS = new Set([
    'paper', 'spigot', 'bukkit', 'purpur',
    'velocity', 'waterfall', 'bungeecord',
]);

export function isPluginLoader(installerType: string | undefined | null): boolean {
    return PLUGIN_LOADERS.has((installerType || '').toLowerCase());
}
