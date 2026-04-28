import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    FirewallAdvancedSettingsSchema,
    FirewallLiveHitsResponseSchema,
    FirewallNATRuleCounterSchema,
    FirewallNATRuleSchema,
    FirewallTrafficRuleCounterSchema,
    FirewallTrafficRuleSchema,
    type FirewallAdvancedSettings,
    type FirewallLiveHitsResponse,
    type FirewallNATRuleCounter,
    type FirewallNATRule,
    type FirewallTrafficRuleCounter,
    type FirewallTrafficRule
} from '$lib/types/network/firewall';
import { apiRequest, isAPIResponse } from '$lib/utils/http';
import z from 'zod/v4';

export interface FirewallReorderRequest {
    id: number;
    priority: number;
}

export async function getFirewallTrafficRules(): Promise<FirewallTrafficRule[] | APIResponse> {
    return await apiRequest('/network/firewall/traffic', FirewallTrafficRuleSchema.array(), 'GET');
}

export async function getFirewallTrafficRuleCounters(): Promise<
    FirewallTrafficRuleCounter[] | APIResponse
> {
    const response = await apiRequest(
        '/network/firewall/traffic/counters',
        APIResponseSchema,
        'GET',
        undefined,
        { raw: true }
    );

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

    const parsed = FirewallTrafficRuleCounterSchema.array().safeParse(response.data);
    if (!parsed.success) {
        return {
            status: 'error',
            message: 'invalid_firewall_traffic_counter_response',
            error: parsed.error.issues.map((issue) => issue.message)
        };
    }

    return parsed.data;
}

export async function createFirewallTrafficRule(
    payload: Partial<FirewallTrafficRule>
): Promise<number | APIResponse> {
    return await apiRequest('/network/firewall/traffic', z.number(), 'POST', payload);
}

export async function updateFirewallTrafficRule(
    id: number,
    payload: Partial<FirewallTrafficRule>
): Promise<APIResponse> {
    return await apiRequest(`/network/firewall/traffic/${id}`, APIResponseSchema, 'PUT', payload);
}

export async function deleteFirewallTrafficRule(id: number): Promise<APIResponse> {
    return await apiRequest(`/network/firewall/traffic/${id}`, APIResponseSchema, 'DELETE');
}

export async function reorderFirewallTrafficRules(
    payload: FirewallReorderRequest[]
): Promise<APIResponse> {
    return await apiRequest('/network/firewall/traffic/reorder', APIResponseSchema, 'PUT', payload);
}

export async function getFirewallNATRules(): Promise<FirewallNATRule[] | APIResponse> {
    return await apiRequest('/network/firewall/nat', FirewallNATRuleSchema.array(), 'GET');
}

export async function createFirewallNATRule(
    payload: Record<string, unknown>
): Promise<number | APIResponse> {
    return await apiRequest('/network/firewall/nat', z.number(), 'POST', payload);
}

export async function getFirewallNATRuleCounters(): Promise<FirewallNATRuleCounter[] | APIResponse> {
    const response = await apiRequest(
        '/network/firewall/nat/counters',
        APIResponseSchema,
        'GET',
        undefined,
        { raw: true }
    );

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

    const parsed = FirewallNATRuleCounterSchema.array().safeParse(response.data);
    if (!parsed.success) {
        return {
            status: 'error',
            message: 'invalid_firewall_nat_counter_response',
            error: parsed.error.issues.map((issue) => issue.message)
        };
    }

    return parsed.data;
}

export async function updateFirewallNATRule(
    id: number,
    payload: Record<string, unknown>
): Promise<APIResponse> {
    return await apiRequest(`/network/firewall/nat/${id}`, APIResponseSchema, 'PUT', payload);
}

export async function deleteFirewallNATRule(id: number): Promise<APIResponse> {
    return await apiRequest(`/network/firewall/nat/${id}`, APIResponseSchema, 'DELETE');
}

export async function reorderFirewallNATRules(payload: FirewallReorderRequest[]): Promise<APIResponse> {
    return await apiRequest('/network/firewall/nat/reorder', APIResponseSchema, 'PUT', payload);
}

export async function getFirewallAdvancedSettings(): Promise<FirewallAdvancedSettings | APIResponse> {
    return await apiRequest('/network/firewall/advanced', FirewallAdvancedSettingsSchema, 'GET');
}

export async function updateFirewallAdvancedSettings(
    preRules: string,
    postRules: string
): Promise<APIResponse> {
    return await apiRequest('/network/firewall/advanced', APIResponseSchema, 'PUT', {
        preRules,
        postRules
    });
}

export async function getFirewallLiveLogs(
    cursor: number,
    limit: number = 200
): Promise<FirewallLiveHitsResponse | APIResponse> {
    const query = new URLSearchParams({
        cursor: String(cursor),
        limit: String(limit)
    });
    const response = await apiRequest(
        `/network/firewall/logs/live?${query.toString()}`,
        APIResponseSchema,
        'GET',
        undefined,
        { raw: true }
    );

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

    const parsed = FirewallLiveHitsResponseSchema.safeParse(response.data);
    if (!parsed.success) {
        return {
            status: 'error',
            message: 'invalid_firewall_live_hits_response',
            error: parsed.error.issues.map((issue) => issue.message)
        };
    }

    return parsed.data;
}
