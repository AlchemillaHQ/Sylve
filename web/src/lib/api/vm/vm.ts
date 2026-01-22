import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    SimpleVmSchema,
    VMDomainSchema,
    VMSchema,
    VMStatSchema,
    type CreateData,
    type SimpleVm,
    type VM,
    type VMDomain,
    type VMStat
} from '$lib/types/vm/vm';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getVmById(id: number, type: 'rid' | 'id'): Promise<VM> {
    return await apiRequest(`/vm/${id}?type=${type}`, VMSchema, 'GET');
}

export async function getVMs(): Promise<VM[]> {
    return await apiRequest('/vm', z.array(VMSchema), 'GET');
}

export async function getSimpleVMs(): Promise<SimpleVm[]> {
    return await apiRequest('/vm/simple', z.array(SimpleVmSchema), 'GET');
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
        vncPort: Number(data.advanced.vncPort),
        vncPassword: data.advanced.vncPassword,
        vncWait: data.advanced.vncWait,
        vncResolution: data.advanced.vncResolution,
        startAtBoot: data.advanced.startAtBoot,
        bootOrder: parseInt(data.advanced.bootOrder.toString(), 10),
        timeOffset: data.advanced.timeOffset,
        cloudInit: data.advanced.cloudInit.enabled,
        cloudInitData: data.advanced.cloudInit.data,
        cloudInitMetadata: data.advanced.cloudInit.metadata,
        ignoreUMSR: data.advanced.ignoreUmsrs
    });
}

export async function deleteVM(
    rid: number,
    deleteMacs: boolean,
    deleteRawDisks: boolean,
    deleteVolumes: boolean
): Promise<APIResponse> {
    return await apiRequest(
        `/vm/${rid}?deletemacs=${deleteMacs}&deleterawdisks=${deleteRawDisks}&deletevolumes=${deleteVolumes}`,
        APIResponseSchema,
        'DELETE'
    );
}

export async function getVMDomain(rid: number | string): Promise<VMDomain> {
    return await apiRequest(`/vm/domain/${rid}`, VMDomainSchema, 'GET');
}

export async function actionVm(rid: number | string, action: string): Promise<APIResponse> {
    return await apiRequest(`/vm/${action}/${rid}`, APIResponseSchema, 'POST');
}

export async function getStats(rid: number, step: string): Promise<VMStat[]> {
    return await apiRequest(`/vm/stats/${rid}/${step}`, z.array(VMStatSchema), 'GET');
}

export async function updateDescription(rid: number, description: string): Promise<APIResponse> {
    return await apiRequest(`/vm/description`, APIResponseSchema, 'PUT', {
        rid,
        description
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
    metadata: string
): Promise<APIResponse> {
    return await apiRequest(`/vm/options/cloud-init/${rid}`, APIResponseSchema, 'PUT', {
        data,
        metadata
    });
}
