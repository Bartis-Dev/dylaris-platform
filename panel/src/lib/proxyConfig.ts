// Helpers for the proxy (BungeeCord/Velocity) Config-tab helper.
//
// The proxy config filename is software-dependent: Velocity uses velocity.toml
// (TOML), while BungeeCord and Waterfall use config.yml (YAML). Backends are
// reached in-network by the stable Docker DNS name mc_<uuid> on the container
// port (default 25565). That name survives restarts and sub-server switches -
// unlike a dynamic container IP - so it is safe to paste into a proxy config.

const DEFAULT_CONTAINER_PORT = 25565;

export function proxyConfigFilename(installerType: string | undefined | null): string {
    return (installerType || '').toLowerCase() === 'velocity' ? 'velocity.toml' : 'config.yml';
}

export function backendAddress(uuid: string, containerPort?: number | null): string {
    const port = containerPort && containerPort > 0 ? containerPort : DEFAULT_CONTAINER_PORT;
    return `mc_${uuid}:${port}`;
}

export function proxyPrereqHint(installerType: string | undefined | null): string {
    if ((installerType || '').toLowerCase() === 'velocity') {
        return 'Each backend needs online-mode=false, enforce-secure-profile=false, and Velocity modern forwarding with a matching forwarding.secret.';
    }
    return 'Each backend needs online-mode=false and BungeeCord support enabled (Spigot spigot.yml bungeecord: true, or the Paper equivalent).';
}
