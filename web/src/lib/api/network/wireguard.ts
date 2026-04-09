import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    WireGuardClientSchema,
    WireGuardServerPeerSchema,
    WireGuardServerSchema,
    type WireGuardClient,
    type WireGuardServer,
    type WireGuardServerPeer
} from '$lib/types/network/wireguard';
import { apiRequest, isAPIResponse } from '$lib/utils/http';

export type WireGuardServerRequest = {
    port: number;
    addresses: string[];
    mtu?: number;
    privateKey?: string;
    allowWireGuardPort?: boolean;
    masqueradeIPv4Interface?: string;
    masqueradeIPv6Interface?: string;
};

export type WireGuardServerPeerRequest = {
    id?: number;
    name: string;
    enabled?: boolean;
    persistentKeepalive?: boolean;
    privateKey?: string;
    preSharedKey?: string;
    clientIPs: string[];
    routableIPs?: string[];
    routeIPs?: boolean;
};

export type WireGuardClientRequest = {
    id?: number;
    name: string;
    enabled?: boolean;
    endpointHost: string;
    endpointPort: number;
    listenPort?: number;
    privateKey: string;
    peerPublicKey: string;
    preSharedKey?: string;
    allowedIPs: string[];
    routeAllowedIPs?: boolean;
    addresses: string[];
    mtu?: number;
    metric?: number;
    fib?: number;
    persistentKeepalive?: boolean;
};

export async function getWireGuardServer(): Promise<WireGuardServer | APIResponse> {
    const response = await apiRequest('/network/wireguard/server', APIResponseSchema, 'GET', undefined, {
        raw: true
    });
    if (!isAPIResponse(response)) {
        return {
            status: 'error',
            message: 'invalid_response',
            error: 'invalid_response_shape'
        };
    }

    if (response.status !== 'success') {
        return response;
    }

    if (response.message === 'wireguard_server_not_initialized' || !response.data) {
        return response;
    }

    const parsed = WireGuardServerSchema.safeParse(response.data);
    if (!parsed.success) {
        return {
            status: 'error',
            message: 'invalid_wireguard_server_response',
            error: parsed.error.issues.map((issue) => issue.message)
        };
    }

    return parsed.data;
}

export async function initWireGuardServer(payload: WireGuardServerRequest): Promise<APIResponse> {
    return apiRequest('/network/wireguard/server', APIResponseSchema, 'POST', payload);
}

export async function editWireGuardServer(payload: WireGuardServerRequest): Promise<APIResponse> {
    return apiRequest('/network/wireguard/server', APIResponseSchema, 'PUT', payload);
}

export async function toggleWireGuardServer(): Promise<APIResponse> {
    return apiRequest('/network/wireguard/server/toggle', APIResponseSchema, 'PUT');
}

export async function deinitWireGuardServer(): Promise<APIResponse> {
    return apiRequest('/network/wireguard/server', APIResponseSchema, 'DELETE');
}

export const wireGuardServerPeers = {
    async add(payload: WireGuardServerPeerRequest): Promise<APIResponse> {
        return apiRequest('/network/wireguard/server/peer', APIResponseSchema, 'POST', payload);
    },

    async edit(payload: WireGuardServerPeerRequest): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/server/peer/${payload.id}`, APIResponseSchema, 'PUT', payload);
    },

    async remove(id: number): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/server/peer/${id}`, APIResponseSchema, 'DELETE');
    },

    async removeBulk(ids: number[]): Promise<APIResponse> {
        return apiRequest('/network/wireguard/server/peer/bulk-delete', APIResponseSchema, 'DELETE', {
            ids
        });
    },

    async toggle(id: number): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/server/peer/toggle/${id}`, APIResponseSchema, 'PUT');
    }
};

export async function getWireGuardClients(): Promise<WireGuardClient[] | APIResponse> {
    const response = await apiRequest('/network/wireguard/clients', APIResponseSchema, 'GET', undefined, {
        raw: true
    });
    if (!isAPIResponse(response)) {
        return {
            status: 'error',
            message: 'invalid_response',
            error: 'invalid_response_shape'
        };
    }

    if (response.status !== 'success') {
        return response;
    }

    const parsed = WireGuardClientSchema.array().safeParse(response.data);
    if (!parsed.success) {
        return {
            status: 'error',
            message: 'invalid_wireguard_clients_response',
            error: parsed.error.issues.map((issue) => issue.message)
        };
    }

    return parsed.data;
}

export const wireGuardClients = {
    async create(payload: WireGuardClientRequest): Promise<APIResponse> {
        return apiRequest('/network/wireguard/clients', APIResponseSchema, 'POST', payload);
    },

    async edit(payload: WireGuardClientRequest): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/clients/${payload.id}`, APIResponseSchema, 'PUT', payload);
    },

    async remove(id: number): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/clients/${id}`, APIResponseSchema, 'DELETE');
    },

    async toggle(id: number): Promise<APIResponse> {
        return apiRequest(`/network/wireguard/clients/toggle/${id}`, APIResponseSchema, 'PUT');
    }
};

export function wireGuardPeerName(peer: WireGuardServerPeer): string {
    const parsed = WireGuardServerPeerSchema.safeParse(peer);
    if (!parsed.success) {
        return `Peer ${peer.id}`;
    }

    return parsed.data.name;
}
