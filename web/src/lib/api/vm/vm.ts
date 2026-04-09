import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    QGAInfoSchema,
    SimpleVmTemplateSchema,
    SimpleVmSchema,
    VMDomainSchema,
    VMLogsSchema,
    VMSchema,
    VMTemplateSchema,
    VMStatSchema,
    type CreateData,
    type QGAInfo,
    type SimpleVm,
    type SimpleVmTemplate,
    type VM,
    type VMLogs,
    type VMTemplate,
    type VMDomain,
    type VMStat,
    type OutcomeResponse,
    OutcomeResponseSchema
} from '$lib/types/vm/vm';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

function toExtraBhyveOptions(raw: string): string[] {
    return raw
        .split('\n')
        .map((line) => line.trim())
        .filter((line) => line.length > 0);
}

export async function getVmById(id: number, type: 'rid' | 'id'): Promise<VM> {
    return await apiRequest(`/vm/${id}?type=${type}`, VMSchema, 'GET');
}

export async function getVMs(hostname?: string): Promise<VM[]> {
    return await apiRequest('/vm', z.array(VMSchema), 'GET', undefined, { hostname });
}

export async function getSimpleVMs(hostname?: string): Promise<SimpleVm[]> {
    return await apiRequest('/vm/simple', z.array(SimpleVmSchema), 'GET', undefined, { hostname });
}

export async function getSimpleVMTemplates(hostname?: string): Promise<SimpleVmTemplate[]> {
    return await apiRequest('/vm/templates/simple', z.array(SimpleVmTemplateSchema), 'GET', undefined, {
        hostname
    });
}

export async function getVMTemplateById(templateId: number, hostname?: string): Promise<VMTemplate> {
    return await apiRequest(`/vm/templates/${templateId}`, VMTemplateSchema, 'GET', undefined, {
        hostname
    });
}

export async function getSimpleVMById(id: number, type: 'rid' | 'id'): Promise<SimpleVm> {
    return await apiRequest(`/vm/simple/${id}?type=${type}`, SimpleVmSchema, 'GET');
}

export async function newVM(data: CreateData): Promise<APIResponse> {
    if (data.storage.iso.toLowerCase() === 'none') {
        data.storage.iso = '';
    }

    return await apiRequest('/vm', APIResponseSchema, 'POST', {
        name: data.name,
        node: data.node,
        description: data.description,
        rid: parseInt(data.id.toString(), 10),
        iso: data.storage.iso,
        storagePool: data.storage.pool,
        storageType: data.storage.type,
        storageSize: data.storage.size,
        storageEmulationType: data.storage.emulation,
        switchName: data.network.switch,
        switchEmulationType: data.network.emulation,
        macId: Number(data.network.mac) || 0,
        cpuSockets: parseInt(data.hardware.sockets.toString(), 10),
        cpuCores: parseInt(data.hardware.cores.toString(), 10),
        cpuThreads: parseInt(data.hardware.threads.toString(), 10),
        cpuPinning: data.hardware.pinnedCPUs,
        ram: parseInt(data.hardware.memory.toString(), 10),
        pciDevices: data.hardware.passthroughIds,
        tpmEmulation: data.advanced.tpmEmulation,
        serial: data.advanced.serial,
        vncEnabled: data.advanced.vncEnabled,
        vncPort: Number(data.advanced.vncPort),
        vncBind: data.advanced.vncBind,
        vncPassword: data.advanced.vncPassword,
        vncWait: data.advanced.vncWait,
        vncResolution: data.advanced.vncResolution,
        startAtBoot: data.advanced.startAtBoot,
        bootOrder: parseInt(data.advanced.bootOrder.toString(), 10),
        timeOffset: data.advanced.timeOffset,
        cloudInit: data.advanced.cloudInit.enabled,
        cloudInitData: data.advanced.cloudInit.data,
        cloudInitMetadata: data.advanced.cloudInit.metadata,
        cloudInitNetworkConfig: data.advanced.cloudInit.networkConfig,
        extraBhyveOptions: toExtraBhyveOptions(data.advanced.extraBhyveOptions),
        ignoreUMSR: data.advanced.ignoreUmsrs,
        qemuGuestAgent: data.advanced.qemuGuestAgent
    });
}

export async function deleteVM(
    rid: number,
    deleteMacs: boolean,
    deleteRawDisks: boolean,
    deleteVolumes: boolean,
    forceDelete: boolean = false
): Promise<APIResponse> {
    return await apiRequest(
        `/vm/${rid}?deletemacs=${deleteMacs}&deleterawdisks=${deleteRawDisks}&deletevolumes=${deleteVolumes}&force=${forceDelete}`,
        APIResponseSchema,
        'DELETE'
    );
}

export async function getVMDomain(rid: number | string): Promise<VMDomain> {
    return await apiRequest(`/vm/domain/${rid}`, VMDomainSchema, 'GET');
}

export async function actionVm(
    rid: number | string,
    action: string,
    hostname?: string
): Promise<OutcomeResponse | APIResponse> {
    return await apiRequest(`/vm/${action}/${rid}`, OutcomeResponseSchema, 'POST', undefined, { hostname });
}

export interface ConvertVMToTemplateRequest {
    name: string;
}

export async function convertVMToTemplate(
    rid: number,
    data: ConvertVMToTemplateRequest,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/vm/templates/convert/${rid}`, APIResponseSchema, 'POST', data, {
        hostname
    });
}

export interface VMTemplateStoragePoolAssignment {
    sourceStorageId: number;
    pool: string;
}

export interface CreateVMFromTemplateRequest {
    mode: 'single' | 'multiple';
    rid?: number;
    name?: string;
    startRid?: number;
    count?: number;
    namePrefix?: string;
    storagePools: VMTemplateStoragePoolAssignment[];
    rewriteCloudInitIdentity?: boolean;
    cloudInitPrefix?: string;
}

export async function createVMFromTemplate(
    templateId: number,
    data: CreateVMFromTemplateRequest,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(`/vm/templates/create/${templateId}`, APIResponseSchema, 'POST', data, {
        hostname
    });
}

export async function deleteVMTemplate(templateId: number, hostname?: string): Promise<APIResponse> {
    return await apiRequest(`/vm/templates/${templateId}`, APIResponseSchema, 'DELETE', undefined, {
        hostname
    });
}

export async function getStats(rid: number, step: string): Promise<VMStat[]> {
    return await apiRequest(`/vm/stats/${rid}/${step}`, z.array(VMStatSchema), 'GET');
}

export async function getVMLogs(rid: number): Promise<VMLogs> {
    return await apiRequest(`/vm/logs/${rid}`, VMLogsSchema, 'GET');
}

export async function updateDescription(rid: number, description: string): Promise<APIResponse> {
    return await apiRequest(`/vm/description`, APIResponseSchema, 'PUT', {
        rid,
        description
    });
}

export async function updateName(rid: number, name: string): Promise<APIResponse> {
    return await apiRequest(`/vm/name`, APIResponseSchema, 'PUT', {
        rid,
        name
    });
}

export async function modifyWoL(rid: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(`/vm/options/wol/${rid}`, APIResponseSchema, 'PUT', {
        enabled
    });
}

export async function modifyIgnoreUMSR(rid: number, ignore: boolean): Promise<APIResponse> {
    return await apiRequest(`/vm/options/ignore-umsrs/${rid}`, APIResponseSchema, 'PUT', {
        ignoreUMSRs: ignore
    });
}

export async function modifyQemuGuestAgent(rid: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(`/vm/options/qemu-guest-agent/${rid}`, APIResponseSchema, 'PUT', {
        enabled
    });
}

export async function modifyTPM(rid: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(`/vm/options/tpm/${rid}`, APIResponseSchema, 'PUT', {
        enabled
    });
}

export async function modifyBootOrder(
    rid: number,
    startAtBoot: boolean,
    bootOrder: number
): Promise<APIResponse> {
    return await apiRequest(`/vm/options/boot-order/${rid}`, APIResponseSchema, 'PUT', {
        startAtBoot,
        bootOrder
    });
}

export async function modifyClockOffset(
    rid: number,
    timeOffset: 'localtime' | 'utc'
): Promise<APIResponse> {
    return await apiRequest(`/vm/options/clock/${rid}`, APIResponseSchema, 'PUT', {
        timeOffset
    });
}

export async function modifySerialConsole(rid: number, enabled: boolean): Promise<APIResponse> {
    return await apiRequest(`/vm/options/serial-console/${rid}`, APIResponseSchema, 'PUT', {
        enabled
    });
}

export async function modifyShutdownWaitTime(rid: number, waitTime: number): Promise<APIResponse> {
    return await apiRequest(`/vm/options/shutdown-wait-time/${rid}`, APIResponseSchema, 'PUT', {
        waitTime
    });
}

export async function modifyCloudInitData(
    rid: number,
    data: string,
    metadata: string,
    networkConfig: string
): Promise<APIResponse> {
    return await apiRequest(`/vm/options/cloud-init/${rid}`, APIResponseSchema, 'PUT', {
        data,
        metadata,
        networkConfig
    });
}

export async function modifyExtraBhyveOptions(
    rid: number,
    extraBhyveOptions: string[]
): Promise<APIResponse> {
    return await apiRequest(`/vm/options/extra-bhyve-options/${rid}`, APIResponseSchema, 'PUT', {
        extraBhyveOptions
    });
}

export async function getQGAInfo(rid: number): Promise<APIResponse | QGAInfo> {
    return await apiRequest(`/vm/qga/${rid}`, QGAInfoSchema, 'GET');
}
