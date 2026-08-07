import {
    APIResponseSchema,
    GuestDeletionResponseSchema,
    type APIResponse,
    type GFSStep,
    type GuestDeletionResponse
} from '$lib/types/common';
import {
    JailLogsSchema,
    JailSchema,
    JailStateSchema,
    JailStatSchema,
    JailStatsBootstrapSchema,
    SimpleJailSchema,
    type CreateData,
    type ExecPhaseKey,
    type ExecPhaseState,
    type Jail,
    type JailLogs,
    type JailStat,
    type JailStatsBootstrap,
    type JailState,
    type SimpleJail,
    JailTemplateSchema,
    type JailTemplate,
    SimpleJailTemplateSchema,
    type SimpleJailTemplate
} from '$lib/types/jail/jail';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
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
        bootstrapName: data.storage.bootstrapName,
        fstab: data.storage.fstab,
        resolvConf: data.network.resolvConf,
        switchName: data.network.switch,
        dhcp: data.network.dhcp,
        slaac: data.network.slaac,
        inheritIPv4: data.network.inheritIPv4,
        inheritIPv6: data.network.inheritIPv6,
        ipv4: data.network.ipv4,
        ipv4Raw: data.network.ipv4Raw,
        ipv4Gw: data.network.ipv4Gateway,
        ipv4GwRaw: data.network.ipv4GatewayRaw,
        ipv6: data.network.ipv6,
        ipv6Raw: data.network.ipv6Raw,
        ipv6Gw: data.network.ipv6Gateway,
        ipv6GwRaw: data.network.ipv6GatewayRaw,
        mac: data.network.mac,
        macRaw: data.network.macRaw,
        vlan: Number(data.network.vlan),
        carp: data.network.carp,
        carpVhid: Number(data.network.carpVhid),
        carpAdvSkew: Number(data.network.carpAdvSkew),
        carpPassword: data.network.carpPassword,
        carpIpv4: data.network.carpIpv4,
        carpIpv4Raw: data.network.carpIpv4Raw,
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

export async function getSimpleJails(
    hostname?: string,
    signal?: AbortSignal
): Promise<SimpleJail[]> {
    return await apiRequest('/jail/simple', z.array(SimpleJailSchema), 'GET', undefined, {
        hostname,
        signal
    });
}

export async function getSimpleJailTemplates(hostname?: string): Promise<SimpleJailTemplate[]> {
    return await apiRequest(
        '/jail/templates/simple',
        z.array(SimpleJailTemplateSchema),
        'GET',
        undefined,
        {
            hostname
        }
    );
}

export async function getJailTemplateById(
    templateId: number,
    hostname?: string
): Promise<JailTemplate> {
    return await apiRequest(`/jail/templates/${templateId}`, JailTemplateSchema, 'GET', undefined, {
        hostname
    });
}

export async function getJails(hostname?: string): Promise<Jail[]> {
    return await apiRequest('/jail', z.array(JailSchema), 'GET', undefined, { hostname });
}

export async function getJailById(
    id: number,
    type: 'ctid' | 'id',
    options?: NodeAPIRequestOptions
): Promise<Jail> {
    return await apiRequest(`/jail/${id}?type=${type}`, JailSchema, 'GET', undefined, options);
}

export async function getSimpleJailById(
    id: number,
    type: 'ctid' | 'id',
    options?: NodeAPIRequestOptions
): Promise<SimpleJail> {
    return await apiRequest(
        `/jail/simple/${id}?type=${type}`,
        SimpleJailSchema,
        'GET',
        undefined,
        options
    );
}

export async function deleteJail(
    ctId: number,
    deleteMacs: boolean,
    deleteRootFs: boolean
): Promise<GuestDeletionResponse> {
    return (await apiRequest(
        `/jail/${ctId}?deletemacs=${deleteMacs}&deleterootfs=${deleteRootFs}`,
        GuestDeletionResponseSchema,
        'DELETE'
    )) as GuestDeletionResponse;
}

export async function getJailStates(): Promise<JailState[]> {
    return await apiRequest('/jail/state', z.array(JailStateSchema), 'GET');
}

export async function getJailStateById(
    ctId: number,
    options?: NodeAPIRequestOptions
): Promise<JailState> {
    return await apiRequest(`/jail/state/${ctId}`, JailStateSchema, 'GET', undefined, options);
}

export async function jailAction(
    ctId: number,
    action: string,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/action/${action}/${ctId}`, APIResponseSchema, 'POST', undefined, {
        hostname
    });
}

export interface ConvertJailToTemplateRequest {
    name: string;
}

export async function convertJailToTemplate(
    ctId: number,
    data: ConvertJailToTemplateRequest,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/templates/convert/${ctId}`, APIResponseSchema, 'POST', data, {
        hostname
    });
}

export interface CreateJailFromTemplateRequest {
    mode: 'single' | 'multiple';
    ctid?: number;
    name?: string;
    startCtid?: number;
    count?: number;
    namePrefix?: string;
    pool?: string;
}

export async function createJailFromTemplate(
    templateId: number,
    data: CreateJailFromTemplateRequest,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/templates/create/${templateId}`, APIResponseSchema, 'POST', data, {
        hostname
    });
}

export async function deleteJailTemplate(
    templateId: number,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/jail/templates/${templateId}`, APIResponseSchema, 'DELETE', undefined, {
        hostname
    });
}

export async function updateDescription(id: number, description: string): Promise<APIResponse> {
    return await apiRequest('/jail/description', APIResponseSchema, 'PUT', {
        id,
        description
    });
}

export async function updateName(id: number, name: string): Promise<APIResponse> {
    return await apiRequest('/jail/name', APIResponseSchema, 'PUT', {
        id,
        name
    });
}

export async function getJailLogs(id: number, options?: NodeAPIRequestOptions): Promise<JailLogs> {
    return await apiRequest(`/jail/${id}/logs`, JailLogsSchema, 'GET', undefined, options);
}

export async function getStats(
    ctId: number,
    step: GFSStep,
    options?: NodeAPIRequestOptions
): Promise<JailStat[] | APIResponse> {
    return await apiRequest(
        `/jail/stats/${ctId}/${step}`,
        z.array(JailStatSchema),
        'GET',
        undefined,
        {
            ...options,
            preserveErrors: true
        }
    );
}

export async function getStatsBootstrap(
    ctId: number,
    options?: NodeAPIRequestOptions
): Promise<JailStatsBootstrap | APIResponse> {
    return await apiRequest(`/jail/stats/${ctId}`, JailStatsBootstrapSchema, 'GET', undefined, {
        ...options,
        preserveErrors: true
    });
}

export async function addNetwork(
    ctId: number,
    name: string,
    switchName: string,
    macId: number,
    macRaw: string,
    ip4: number,
    ip4Raw: string,
    ip4gw: number,
    ip4gwRaw: string,
    ip6: number,
    ip6Raw: string,
    ip6gw: number,
    ip6gwRaw: string,
    dhcp: boolean,
    slaac: boolean,
    defaultGateway: boolean,
    vlan: Number,
    carp: boolean,
    carpVhid: number,
    carpAdvSkew: number,
    carpPassword: string,
    carpIpv4: number,
    carpIpv4Raw: string

): Promise<APIResponse> {
    return await apiRequest('/jail/network', APIResponseSchema, 'POST', {
        ctId,
        name,
        switchName,
        macId,
        macRaw,
        ip4,
        ip4Raw,
        ip4gw,
        ip4gwRaw,
        ip6,
        ip6Raw,
        ip6gw,
        ip6gwRaw,
        dhcp,
        slaac,
        defaultGateway,
        vlan,
        carp,
        carpVhid,
        carpAdvSkew,
        carpPassword,
        carpIpv4,
        carpIpv4Raw
    });
}

export async function updateNetwork(
    networkId: number,
    name: string,
    switchName: string,
    macId: number,
    macRaw: string,
    ip4: number,
    ip4Raw: string,
    ip4gw: number,
    ip4gwRaw: string,
    ip6: number,
    ip6Raw: string,
    ip6gw: number,
    ip6gwRaw: string,
    dhcp: boolean,
    slaac: boolean,
    defaultGateway: boolean,
    vlan: number,
    carp: boolean,
    carpVhid: number,
    carpAdvSkew: number,
    carpPassword: string,
    carpIpv4: number,
    carpIpv4Raw: string

): Promise<APIResponse> {
    return await apiRequest('/jail/network', APIResponseSchema, 'PUT', {
        networkId,
        name,
        switchName,
        macId,
        macRaw,
        ip4,
        ip4Raw,
        ip4gw,
        ip4gwRaw,
        ip6,
        ip6Raw,
        ip6gw,
        ip6gwRaw,
        dhcp,
        slaac,
        defaultGateway,
        vlan,
        carp,
        carpVhid,
        carpAdvSkew,
        carpPassword,
        carpIpv4,
        carpIpv4Raw
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
    const s = ipv4 === false && ipv6 === false ? 'disinheritance' : 'inheritance';

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

export async function modifyWoL(ctId: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(`/jail/options/wol/${ctId}`, APIResponseSchema, 'PUT', {
        enabled
    });
}

export async function modifyFstab(ctId: number, fstab: string): Promise<APIResponse> {
    return await apiRequest(`/jail/options/fstab/${ctId}`, APIResponseSchema, 'PUT', {
        fstab
    });
}

export async function modifyResolvConf(ctId: number, resolvConf: string): Promise<APIResponse> {
    return await apiRequest(`/jail/options/resolv-conf/${ctId}`, APIResponseSchema, 'PUT', {
        resolvConf
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
