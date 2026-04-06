export interface WireGuardKeypair {
    privateKey: string;
    publicKey: string;
}

function base64UrlToBytes(input: string): Uint8Array {
    const base64 = input.replace(/-/g, '+').replace(/_/g, '/')
        + '='.repeat((4 - (input.length % 4)) % 4);

    const binary = atob(base64);
    const bytes = new Uint8Array(binary.length);

    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }

    return bytes;
}

function bytesToBase64(bytes: Uint8Array): string {
    let binary = '';
    for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
}

function randomBytes(length: number): Uint8Array {
    const bytes = new Uint8Array(length);
    crypto.getRandomValues(bytes);
    return bytes;
}

export async function generateKeypair(): Promise<WireGuardKeypair> {
    const keyPair = await crypto.subtle.generateKey(
        { name: 'X25519' },
        true,
        ['deriveBits']
    ) as CryptoKeyPair;

    const privateJwk = await crypto.subtle.exportKey('jwk', keyPair.privateKey);
    const publicJwk = await crypto.subtle.exportKey('jwk', keyPair.publicKey);

    if (!privateJwk.d || !publicJwk.x) {
        throw new Error('Failed to export X25519 keypair');
    }

    const privateKey = base64UrlToBytes(privateJwk.d);
    const publicKey = base64UrlToBytes(publicJwk.x);

    if (privateKey.length !== 32 || publicKey.length !== 32) {
        throw new Error('Unexpected X25519 key length');
    }

    return {
        privateKey: bytesToBase64(privateKey),
        publicKey: bytesToBase64(publicKey)
    };
}

export function generatePresharedKey(): string {
    return bytesToBase64(randomBytes(32));
}

function ipv4ToInt(ip: string): number {
    return ip.split('.').reduce((acc, octet) => ((acc << 8) | parseInt(octet)) >>> 0, 0) >>> 0;
}

function intToIPv4(n: number): string {
    return [(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff].join('.');
}

function expandIPv6(ip: string): string {
    if (ip.includes('::')) {
        const [left, right] = ip.split('::');
        const leftParts = left ? left.split(':') : [];
        const rightParts = right ? right.split(':') : [];
        const missing = 8 - leftParts.length - rightParts.length;
        const middle = Array(missing).fill('0000');
        return [...leftParts, ...middle, ...rightParts].map((p) => p.padStart(4, '0')).join(':');
    }
    return ip
        .split(':')
        .map((p) => p.padStart(4, '0'))
        .join(':');
}

function ipv6ToBigInt(ip: string): bigint {
    return BigInt('0x' + expandIPv6(ip).replace(/:/g, ''));
}

function bigIntToIPv6(n: bigint): string {
    const hex = n.toString(16).padStart(32, '0');
    const groups = (hex.match(/.{4}/g) as string[]).map((g) => parseInt(g, 16));

    // Find the longest run of consecutive zero groups for :: compression
    let bestStart = -1, bestLen = 0, curStart = -1, curLen = 0;
    for (let i = 0; i <= groups.length; i++) {
        if (i < groups.length && groups[i] === 0) {
            if (curStart === -1) { curStart = i; curLen = 0; }
            curLen++;
        } else {
            if (curLen > bestLen) { bestStart = curStart; bestLen = curLen; }
            curStart = -1; curLen = 0;
        }
    }

    if (bestLen < 2) {
        return groups.map((g) => g.toString(16)).join(':');
    }

    const before = groups.slice(0, bestStart).map((g) => g.toString(16)).join(':');
    const after = groups.slice(bestStart + bestLen).map((g) => g.toString(16)).join(':');
    return `${before}::${after}`;
}

import type { WireGuardServer, WireGuardServerPeer } from '$lib/types/network/wireguard';

// Returns the network CIDRs derived from the server's interface addresses.
// e.g. 10.10.0.1/24 → 10.10.0.0/24, fd00::1/64 → fd00::/64
export function getWireGuardSubnets(server: WireGuardServer): string[] {
    return server.addresses.map((addr) => {
        const [ip, prefixStr] = addr.split('/');
        const prefix = parseInt(prefixStr);
        if (ip.includes(':')) {
            const big = ipv6ToBigInt(ip);
            const hostBits = BigInt(128 - prefix);
            const mask = (1n << hostBits) - 1n;
            const network = big & ~mask;
            return `${bigIntToIPv6(network)}/${prefix}`;
        } else {
            const int = ipv4ToInt(ip);
            const shift = 32 - prefix;
            const mask = shift === 32 ? 0 : ((0xffffffff << shift) >>> 0);
            const network = (int & mask) >>> 0;
            return `${intToIPv4(network)}/${prefix}`;
        }
    });
}

export function generatePeerConfig(
    server: WireGuardServer,
    peer: WireGuardServerPeer,
    dns: string[],
    endpoint: string,
    allowedIPs: string[],
    persistentKeepalive: boolean = false
): string {
    const lines: string[] = [
        '[Interface]',
        `PrivateKey = ${peer.privateKey}`,
        `Address = ${peer.clientIPs.join(', ')}`
    ];

    if (dns.length > 0) {
        lines.push(`DNS = ${dns.join(', ')}`);
    }

    lines.push('', '[Peer]', `PublicKey = ${server.publicKey}`);

    if (peer.preSharedKey) {
        lines.push(`PresharedKey = ${peer.preSharedKey}`);
    }

    if (endpoint) {
        lines.push(`Endpoint = ${endpoint}`);
    }

    lines.push(`AllowedIPs = ${allowedIPs.join(', ') || '0.0.0.0/0, ::/0'}`);

    if (persistentKeepalive) {
        lines.push('PersistentKeepalive = 25');
    }

    return lines.join('\n');
}

export function generateNextClientIPs(server: WireGuardServer): string[] {
    const usedIPv4 = new Set<number>();
    const usedIPv6 = new Set<string>();

    for (const peer of server.peers) {
        for (const clientIP of peer.clientIPs) {
            const [ip] = clientIP.split('/');
            if (ip.includes(':')) {
                try { usedIPv6.add(ipv6ToBigInt(ip).toString()); } catch { /* skip */ }
            } else {
                usedIPv4.add(ipv4ToInt(ip));
            }
        }
    }

    const result: string[] = [];

    for (const addr of server.addresses) {
        const [ip, prefixStr] = addr.split('/');
        const prefix = parseInt(prefixStr);

        if (ip.includes(':')) {
            const serverBig = ipv6ToBigInt(ip);
            usedIPv6.add(serverBig.toString());
            const hostBits = BigInt(128 - prefix);
            const mask = (1n << hostBits) - 1n;
            const network = serverBig & ~mask;
            let candidate = network + 1n;
            const max = network | mask;
            while (candidate < max) {
                if (!usedIPv6.has(candidate.toString())) {
                    result.push(`${bigIntToIPv6(candidate)}/128`);
                    break;
                }
                candidate++;
            }
        } else {
            const serverInt = ipv4ToInt(ip);
            usedIPv4.add(serverInt);
            const shift = 32 - prefix;
            const mask = shift === 32 ? 0 : ((0xffffffff << shift) >>> 0);
            const network = (serverInt & mask) >>> 0;
            const broadcast = (network | (~mask >>> 0)) >>> 0;
            let candidate = (network + 1) >>> 0;
            while (candidate < broadcast) {
                if (!usedIPv4.has(candidate)) {
                    result.push(`${intToIPv4(candidate)}/32`);
                    break;
                }
                candidate = (candidate + 1) >>> 0;
            }
        }
    }

    return result;
}