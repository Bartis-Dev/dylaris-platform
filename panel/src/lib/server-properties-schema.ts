// Declarative schema for Minecraft server.properties — drives the Simple
// mode of the Configuration tab. Adding a new property here is enough;
// new UI controls render automatically.

export type PropertyGroup =
    | 'world'
    | 'network'
    | 'pvp'
    | 'performance'
    | 'resourcepack'
    | 'motd'
    | 'whitelist'
    | 'misc';

export interface BasePropertyDef {
    key: string;
    group: PropertyGroup;
    label: string;
    description?: string;
    requiresRestart?: boolean;
}

export interface ToggleProperty extends BasePropertyDef {
    type: 'toggle';
    default: boolean;
}

export interface TextProperty extends BasePropertyDef {
    type: 'text';
    default: string;
    placeholder?: string;
}

export interface TextareaProperty extends BasePropertyDef {
    type: 'textarea';
    default: string;
    maxLength?: number;
}

export interface NumberProperty extends BasePropertyDef {
    type: 'number';
    default: number;
    min?: number;
    max?: number;
}

export interface SliderProperty extends BasePropertyDef {
    type: 'slider';
    default: number;
    min: number;
    max: number;
    step?: number;
}

export interface SelectProperty extends BasePropertyDef {
    type: 'select';
    default: string;
    options: { value: string; label: string }[];
}

export type PropertyDef =
    | ToggleProperty
    | TextProperty
    | TextareaProperty
    | NumberProperty
    | SliderProperty
    | SelectProperty;

export const GROUP_LABELS: Record<PropertyGroup, string> = {
    world: 'World',
    network: 'Network',
    pvp: 'PvP & Spawning',
    performance: 'Performance',
    resourcepack: 'Resource Pack',
    motd: 'MOTD',
    whitelist: 'Whitelist & Access',
    misc: 'Misc',
};

// Vanilla schema. Paper/Spigot can layer additional entries on top later.
export const VANILLA_SCHEMA: PropertyDef[] = [
    // World
    { key: 'level-name', type: 'text', default: 'world', group: 'world', label: 'World Name', description: 'Folder name on disk and identifier the server uses.', requiresRestart: true },
    { key: 'level-seed', type: 'text', default: '', group: 'world', label: 'World Seed', placeholder: 'Empty = random', requiresRestart: true },
    {
        key: 'level-type', type: 'select', default: 'minecraft:normal', group: 'world', label: 'World Type', requiresRestart: true,
        options: [
            { value: 'minecraft:normal', label: 'Normal' },
            { value: 'minecraft:flat', label: 'Superflat' },
            { value: 'minecraft:large_biomes', label: 'Large Biomes' },
            { value: 'minecraft:amplified', label: 'Amplified' },
        ],
    },
    {
        key: 'gamemode', type: 'select', default: 'survival', group: 'world', label: 'Default Gamemode',
        options: [
            { value: 'survival', label: 'Survival' },
            { value: 'creative', label: 'Creative' },
            { value: 'adventure', label: 'Adventure' },
            { value: 'spectator', label: 'Spectator' },
        ],
    },
    { key: 'force-gamemode', type: 'toggle', default: false, group: 'world', label: 'Force Gamemode', description: 'Forces players into the default gamemode on join.' },
    {
        key: 'difficulty', type: 'select', default: 'easy', group: 'world', label: 'Difficulty',
        options: [
            { value: 'peaceful', label: 'Peaceful' },
            { value: 'easy', label: 'Easy' },
            { value: 'normal', label: 'Normal' },
            { value: 'hard', label: 'Hard' },
        ],
    },
    { key: 'hardcore', type: 'toggle', default: false, group: 'world', label: 'Hardcore', description: 'Permadeath: dead players are banned from the world.' },
    { key: 'allow-nether', type: 'toggle', default: true, group: 'world', label: 'Allow Nether' },
    { key: 'allow-flight', type: 'toggle', default: false, group: 'world', label: 'Allow Flight', description: 'Permits non-creative players to fly (e.g. via mods).' },

    // Network
    { key: 'server-port', type: 'number', default: 25565, group: 'network', label: 'Server Port', min: 1, max: 65535, requiresRestart: true },
    { key: 'server-ip', type: 'text', default: '', group: 'network', label: 'Server IP', placeholder: 'Empty = bind all', requiresRestart: true },
    { key: 'online-mode', type: 'toggle', default: true, group: 'network', label: 'Online Mode', description: 'Verifies Mojang authentication. Disable only for cracked/offline servers.' },
    { key: 'prevent-proxy-connections', type: 'toggle', default: false, group: 'network', label: 'Prevent Proxy Connections' },
    { key: 'enable-query', type: 'toggle', default: false, group: 'network', label: 'Enable Query Protocol' },
    { key: 'query.port', type: 'number', default: 25565, group: 'network', label: 'Query Port', min: 1, max: 65535 },
    { key: 'enable-rcon', type: 'toggle', default: false, group: 'network', label: 'Enable RCON' },
    { key: 'rcon.port', type: 'number', default: 25575, group: 'network', label: 'RCON Port', min: 1, max: 65535 },
    { key: 'rcon.password', type: 'text', default: '', group: 'network', label: 'RCON Password' },

    // PvP & Spawning
    { key: 'pvp', type: 'toggle', default: true, group: 'pvp', label: 'PvP' },
    { key: 'spawn-protection', type: 'number', default: 16, group: 'pvp', label: 'Spawn Protection (blocks)', min: 0, max: 1024 },
    { key: 'spawn-animals', type: 'toggle', default: true, group: 'pvp', label: 'Spawn Animals' },
    { key: 'spawn-monsters', type: 'toggle', default: true, group: 'pvp', label: 'Spawn Monsters' },
    { key: 'spawn-npcs', type: 'toggle', default: true, group: 'pvp', label: 'Spawn NPCs (Villagers)' },

    // Performance
    { key: 'max-players', type: 'number', default: 20, group: 'performance', label: 'Max Players', min: 1, max: 10000 },
    { key: 'view-distance', type: 'slider', default: 10, group: 'performance', label: 'View Distance (chunks)', min: 4, max: 32, requiresRestart: true },
    { key: 'simulation-distance', type: 'slider', default: 10, group: 'performance', label: 'Simulation Distance (chunks)', min: 4, max: 32, requiresRestart: true },
    { key: 'network-compression-threshold', type: 'number', default: 256, group: 'performance', label: 'Network Compression Threshold (bytes)', min: -1, max: 65536 },
    { key: 'max-tick-time', type: 'number', default: 60000, group: 'performance', label: 'Max Tick Time (ms)', min: -1, max: 600000 },
    { key: 'sync-chunk-writes', type: 'toggle', default: true, group: 'performance', label: 'Sync Chunk Writes' },

    // Resource Pack
    { key: 'resource-pack', type: 'text', default: '', group: 'resourcepack', label: 'Resource Pack URL', placeholder: 'https://example.com/pack.zip' },
    { key: 'resource-pack-sha1', type: 'text', default: '', group: 'resourcepack', label: 'Resource Pack SHA-1', placeholder: 'Optional integrity check' },
    { key: 'resource-pack-prompt', type: 'text', default: '', group: 'resourcepack', label: 'Resource Pack Prompt' },
    { key: 'require-resource-pack', type: 'toggle', default: false, group: 'resourcepack', label: 'Require Resource Pack', description: 'Disconnect players who refuse the resource pack.' },

    // MOTD
    { key: 'motd', type: 'textarea', default: 'A Minecraft Server', group: 'motd', label: 'Message of the Day', maxLength: 256, description: 'Shown in the server list. Use § for color codes.' },
    { key: 'enable-status', type: 'toggle', default: true, group: 'motd', label: 'Show in Server List' },

    // Whitelist & Access
    { key: 'white-list', type: 'toggle', default: false, group: 'whitelist', label: 'Whitelist Enabled' },
    { key: 'enforce-whitelist', type: 'toggle', default: false, group: 'whitelist', label: 'Enforce Whitelist', description: 'Kicks online players removed from the whitelist.' },
    { key: 'op-permission-level', type: 'slider', default: 4, group: 'whitelist', label: 'Default OP Permission Level', min: 1, max: 4 },
    { key: 'function-permission-level', type: 'slider', default: 2, group: 'whitelist', label: 'Function Permission Level', min: 1, max: 4 },

    // Misc
    { key: 'broadcast-console-to-ops', type: 'toggle', default: true, group: 'misc', label: 'Broadcast Console to Ops' },
    { key: 'broadcast-rcon-to-ops', type: 'toggle', default: true, group: 'misc', label: 'Broadcast RCON to Ops' },
    { key: 'enable-command-block', type: 'toggle', default: false, group: 'misc', label: 'Enable Command Blocks' },
    { key: 'snooper-enabled', type: 'toggle', default: false, group: 'misc', label: 'Snooper (telemetry) Enabled' },
];

export function getSchemaForSoftware(_software?: string): PropertyDef[] {
    // Paper/Spigot share Vanilla's properties + extras. Until we ship a
    // dedicated schema, the Vanilla one covers ~95% of common knobs.
    return VANILLA_SCHEMA;
}

export function groupedSchema(schema: PropertyDef[]): Record<PropertyGroup, PropertyDef[]> {
    const out: Partial<Record<PropertyGroup, PropertyDef[]>> = {};
    for (const def of schema) {
        if (!out[def.group]) out[def.group] = [];
        out[def.group]!.push(def);
    }
    return out as Record<PropertyGroup, PropertyDef[]>;
}
