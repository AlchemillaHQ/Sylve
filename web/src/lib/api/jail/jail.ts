import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    JailLogsSchema,
    JailSchema,
    JailStateSchema,
    JailStatSchema,
    SimpleJailSchema,
    type CreateData,
    type ExecPhaseKey,
    type ExecPhaseState,
    type Jail,
    type JailLogs,
    type JailStat,
    type JailState,
    type SimpleJail
} from '$lib/types/jail/jail';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function newJail(data: CreateData): Promise<APIResponse> {
    return await apiRequest('/jail', APIResponseSchema, 'POST', {
        name: data.name,
        hostname: data.hostname,
        node: data.node,
        ctId: Number(data.id.toString()),
        description: data.description,
        pool: data.storage.pool,
        base: data.storage.base,
        fstab: data.storage.fstab,
        switchName: data.network.switch,
        dhcp: data.network.dhcp,
        slaac: data.network.slaac,
        inheritIPv4: data.network.inheritIPv4,
        inheritIPv6: data.network.inheritIPv6,
        ipv4: data.network.ipv4,
        ipv4Gw: data.network.ipv4Gateway,
        ipv6: data.network.ipv6,
        ipv6Gw: data.network.ipv6Gateway,
        mac: data.network.mac,
        resourceLimits: data.hardware.resourceLimits,
        cores: Number(data.hardware.cpuCores.toString()),
        memory: Number(data.hardware.ram.toString()),
        startAtBoot: data.hardware.startAtBoot,
        startOrder: Number(data.hardware.bootOrder),
        devfsRuleset: data.hardware.devfsRuleset,
        jailType: data.advanced.jailType,
        additionalOptions: data.advanced.additionalOptions,
        allowedOptions: data.advanced.allowedOptions,
        hooks: data.advanced.execScripts,
        cleanEnvironment: data.advanced.cleanEnvironment,
        type: data.advanced.jailType,
        metadataMeta: data.advanced.metadata.meta,
        metadataEnv: data.advanced.metadata.env
    });
}

export async function getSimpleJails(): Promise<SimpleJail[]> {
    return await apiRequest('/jail/simple', z.array(SimpleJailSchema), 'GET');
}

export async function getJails(): Promise<Jail[]> {
    return await apiRequest('/jail', z.array(JailSchema), 'GET');
}

export async function getJailById(id: number, type: 'ctid' | 'id'): Promise<Jail> {
    return await apiRequest(`/jail/${id}?type=${type}`, JailSchema, 'GET');
}

export async function deleteJail(
    ctId: number,
    deleteMacs: boolean,
    deleteRootFs: boolean
): Promise<APIResponse> {
    return await apiRequest(
        `/jail/${ctId}?deletemacs=${deleteMacs}&deleterootfs=${deleteRootFs}`,
        APIResponseSchema,
        'DELETE'
    );
}

export async function getJailStates(): Promise<JailState[]> {
    return await apiRequest('/jail/state', z.array(JailStateSchema), 'GET');
}

export async function getJailStateById(ctId: number): Promise<JailState> {
    return await apiRequest(`/jail/state/${ctId}`, JailStateSchema, 'GET');
}

export async function jailAction(ctId: number, action: string): Promise<APIResponse> {
    return await apiRequest(`/jail/action/${action}/${ctId}`, APIResponseSchema, 'POST');
}

export async function updateDescription(id: number, description: string): Promise<APIResponse> {
    return await apiRequest('/jail/description', APIResponseSchema, 'PUT', {
        id,
        description
    });
}

export async function getJailLogs(id: number): Promise<JailLogs> {
    return await apiRequest(`/jail/${id}/logs`, JailLogsSchema, 'GET');
}

export async function getStats(ctId: number, step: string): Promise<JailStat[]> {
    return await apiRequest(`/jail/stats/${ctId}/${step}`, z.array(JailStatSchema), 'GET');
}

export async function addNetwork(
    ctId: number,
    name: string,
    switchName: string,
    macId: number,
    ip4: number,
    ip4gw: number,
    ip6: number,
    ip6gw: number,
    dhcp: boolean,
    slaac: boolean,
    defaultGateway: boolean
): Promise<APIResponse> {
    return await apiRequest('/jail/network', APIResponseSchema, 'POST', {
        ctId,
        name,
        switchName,
        macId,
        ip4,
        ip4gw,
        ip6,
        ip6gw,
        dhcp,
        slaac,
        defaultGateway
    });
}

export async function deleteNetwork(ctId: number, networkId: number): Promise<APIResponse> {
    return await apiRequest(`/jail/network/${ctId}/${networkId}`, APIResponseSchema, 'DELETE');
}

export async function updateResourceLimits(ctId: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(
        `/jail/resource-limits/${ctId}?enabled=${enabled}`,
        APIResponseSchema,
        'PUT'
    );
}

export async function setNetworkInheritance(
    ctId: number,
    ipv4: boolean,
    ipv6: boolean
): Promise<APIResponse> {
    let s = ipv4 === false && ipv6 === false ? 'disinheritance' : 'inheritance';

    return await apiRequest(`/jail/network/${s}/${ctId}`, APIResponseSchema, 'PUT', {
        ipv4,
        ipv6
    });
}

export async function modifyBootOrder(
    ctId: number,
    startAtBoot: boolean,
    bootOrder: number
): Promise<APIResponse> {
    return await apiRequest(`/jail/options/boot-order/${ctId}`, APIResponseSchema, 'PUT', {
        startAtBoot,
        bootOrder
    });
}

export async function modifyFstab(ctId: number, fstab: string): Promise<APIResponse> {
    return await apiRequest(`/jail/options/fstab/${ctId}`, APIResponseSchema, 'PUT', {
        fstab
    });
}

export async function modifyDevFSRules(ctId: number, devFSRules: string): Promise<APIResponse> {
    return await apiRequest(`/jail/options/devfs-rules/${ctId}`, APIResponseSchema, 'PUT', {
        devFSRules
    });
}

export async function modifyAdditionalOptions(
    ctId: number,
    additionalOptions: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/options/additional-options/${ctId}`, APIResponseSchema, 'PUT', {
        additionalOptions
    });
}

export async function modifyAllowedOptions(
    ctId: number,
    allowedOptions: string[]
): Promise<APIResponse> {
    return await apiRequest(`/jail/options/allowed-options/${ctId}`, APIResponseSchema, 'PUT', {
        allowedOptions
    });
}

export async function modifyMetadata(
    ctId: number,
    metadata: string,
    env: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/options/metadata/${ctId}`, APIResponseSchema, 'PUT', {
        metadata,
        env
    });
}

export async function modifyLifecycleHooks(
    ctId: number,
    hooks: Record<ExecPhaseKey, ExecPhaseState>
): Promise<APIResponse> {
    return await apiRequest(`/jail/options/lifecycle-hooks/${ctId}`, APIResponseSchema, 'PUT', {
        hooks
    });
}
