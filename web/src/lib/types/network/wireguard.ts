import { z } from 'zod/v4';

export const WireGuardServerPeerSchema = z.object({
    id: z.number().int(),
    name: z.string(),
    enabled: z.boolean(),
    wireguardServerId: z.number().int(),
    privateKey: z.string(),
    publicKey: z.string(),
    preSharedKey: z.string(),
    clientIPs: z.array(z.string()),
    routableIPs: z.array(z.string()).nullish().transform((value) => value ?? []),
    routeIPs: z.boolean(),
    persistentKeepalive: z.boolean(),
    lastHandshake: z.string(),
    rx: z.number().int().nonnegative(),
    tx: z.number().int().nonnegative(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export const WireGuardServerSchema = z.object({
    id: z.number().int(),
    enabled: z.boolean(),
    port: z.number().int(),
    addresses: z.array(z.string()),
    allowWireGuardPort: z.boolean().nullish().transform((value) => value ?? false),
    masqueradeIPv4Interface: z.string().nullish().transform((value) => value ?? ''),
    masqueradeIPv6Interface: z.string().nullish().transform((value) => value ?? ''),
    privateKey: z.string(),
    publicKey: z.string(),
    peers: z.array(WireGuardServerPeerSchema),
    mtu: z.number().int(),
    metric: z.number().int(),
    rx: z.number().int().nonnegative(),
    tx: z.number().int().nonnegative(),
    uptime: z.number().int().nonnegative(),
    lastHandshake: z.string(),
    restartedAt: z.string(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export const WireGuardClientSchema = z.object({
    id: z.number().int(),
    enabled: z.boolean(),
    name: z.string(),
    endpointHost: z.string(),
    endpointPort: z.number().int(),
    listenPort: z.number().int(),
    privateKey: z.string(),
    publicKey: z.string(),
    peerPublicKey: z.string(),
    preSharedKey: z.string(),
    allowedIPs: z.array(z.string()),
    addresses: z.array(z.string()),
    routeAllowedIPs: z.boolean(),
    mtu: z.number().int(),
    metric: z.number().int(),
    fib: z.number().int(),
    persistentKeepalive: z.boolean(),
    rx: z.number().int().nonnegative(),
    tx: z.number().int().nonnegative(),
    uptime: z.number().int().nonnegative(),
    lastHandshake: z.string(),
    restartedAt: z.string(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export const WireGuardClientStatusSchema = z.enum(['active', 'idle', 'disconnected', 'disabled']);

export type WireGuardServer = z.infer<typeof WireGuardServerSchema>;
export type WireGuardServerPeer = z.infer<typeof WireGuardServerPeerSchema>;
export type WireGuardClient = z.infer<typeof WireGuardClientSchema>;
export type WireGuardClientStatus = z.infer<typeof WireGuardClientStatusSchema>;

export function wireGuardClientStatus(client: WireGuardClient): WireGuardClientStatus {
    if (!client.enabled) {
        return 'disabled';
    }

    const handshake = Date.parse(client.lastHandshake);
    if (Number.isNaN(handshake) || handshake <= Date.parse('2001-01-01T00:00:00Z')) {
        return 'idle';
    }

    const ageSeconds = (Date.now() - handshake) / 1000;
    if (ageSeconds > 180) {
        return 'disconnected';
    }

    return 'active';
}
