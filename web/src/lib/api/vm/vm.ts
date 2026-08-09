import {
	APIResponseSchema,
	GuestDeletionResponseSchema,
	type APIResponse,
	type GFSStep,
	type GuestDeletionResponse
} from '$lib/types/common';
import {
	QGAInfoSchema,
	SimpleVmTemplateSchema,
	SimpleVmSchema,
	VMDomainSchema,
	VMLogsSchema,
	VMSchema,
	VMTemplateSchema,
	VMTemplateCaptureTaskResponseSchema,
	VMTemplateInstantiationTaskResponseSchema,
	VMStatSchema,
	VMStatsBootstrapSchema,
	type CreateData,
	type QGAInfo,
	type SimpleVm,
	type SimpleVmTemplate,
	type VM,
	type VMLogs,
	type VMTemplate,
	type VMTemplateCaptureTaskResponse,
	type VMTemplateInstantiationTaskResponse,
	type VMDomain,
	type VMStat,
	type VMStatsBootstrap,
	type VMActionResponse,
	type VMLifecycleAction,
	VMActionResponseSchema
} from '$lib/types/vm/vm';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

function toExtraBhyveOptions(raw: string): string[] {
	return raw
		.split('\n')
		.map((line) => line.trim())
		.filter((line) => line.length > 0);
}

export async function getVmById(rid: number, options?: NodeAPIRequestOptions): Promise<VM> {
	return await apiRequest(`/vm/${rid}`, VMSchema, 'GET', undefined, options);
}

export async function getVmByIdResult(
	rid: number,
	options?: NodeAPIRequestOptions
): Promise<VM | APIResponse> {
	return await apiRequest(`/vm/${rid}`, VMSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getVMs(hostname?: string): Promise<VM[]> {
	return await apiRequest('/vm', z.array(VMSchema), 'GET', undefined, { hostname });
}

export async function getVMsResult(options?: NodeAPIRequestOptions): Promise<VM[] | APIResponse> {
	return await apiRequest('/vm', z.array(VMSchema), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getSimpleVMs(hostname?: string, signal?: AbortSignal): Promise<SimpleVm[]> {
	return await apiRequest('/vm/simple', z.array(SimpleVmSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getSimpleVMTemplates(hostname?: string): Promise<SimpleVmTemplate[]> {
	return await apiRequest('/vm/templates', z.array(SimpleVmTemplateSchema), 'GET', undefined, {
		hostname
	});
}

export async function getVMTemplateById(
	templateId: number,
	hostname?: string
): Promise<VMTemplate> {
	return await apiRequest(`/vm/templates/${templateId}`, VMTemplateSchema, 'GET', undefined, {
		hostname
	});
}

export async function getSimpleVMById(
	rid: number,
	options?: NodeAPIRequestOptions
): Promise<SimpleVm> {
	return await apiRequest(`/vm/simple/${rid}`, SimpleVmSchema, 'GET', undefined, options);
}

export async function newVM(data: CreateData, hostname: string): Promise<APIResponse> {
	const iso = data.storage.iso.toLowerCase() === 'none' ? '' : data.storage.iso;

	return await apiRequest(
		'/vm',
		APIResponseSchema,
		'POST',
		{
			name: data.name,
			description: data.description,
			rid: parseInt(data.id.toString(), 10),
			iso,
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
			startOrder: parseInt(data.advanced.bootOrder.toString(), 10),
			timeOffset: data.advanced.timeOffset,
			bootRom: data.advanced.bootRom,
			cloudInit: data.advanced.cloudInit.enabled,
			cloudInitData: data.advanced.cloudInit.data,
			cloudInitMetadata: data.advanced.cloudInit.metadata,
			cloudInitNetworkConfig: data.advanced.cloudInit.networkConfig,
			extraBhyveOptions: toExtraBhyveOptions(data.advanced.extraBhyveOptions),
			ignoreUMSR: data.advanced.ignoreUmsrs,
			qemuGuestAgent: data.advanced.qemuGuestAgent
		},
		{ hostname }
	);
}

export async function deleteVM(
	rid: number,
	deleteMacs: boolean,
	deleteRawDisks: boolean,
	deleteVolumes: boolean,
	hostname?: string
): Promise<GuestDeletionResponse> {
	return (await apiRequest(
		`/vm/${rid}?deletemacs=${deleteMacs}&deleterawdisks=${deleteRawDisks}&deletevolumes=${deleteVolumes}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	)) as GuestDeletionResponse;
}

export async function forceDeleteVM(
	rid: number,
	deleteMacs: boolean = true,
	hostname?: string
): Promise<GuestDeletionResponse> {
	return (await apiRequest(
		`/vm/${rid}?force=true&deletemacs=${deleteMacs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	)) as GuestDeletionResponse;
}

export async function purgeVMRegistration(
	rid: number,
	deleteMacs: boolean = true,
	hostname?: string
): Promise<GuestDeletionResponse> {
	return (await apiRequest(
		`/vm/${rid}/registration?deletemacs=${deleteMacs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	)) as GuestDeletionResponse;
}

export async function getVMDomain(
	rid: number | string,
	options?: NodeAPIRequestOptions
): Promise<VMDomain | APIResponse> {
	return await apiRequest(`/vm/${rid}/domain`, VMDomainSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function actionVm(
	rid: number | string,
	action: VMLifecycleAction,
	hostname?: string
): Promise<VMActionResponse | APIResponse> {
	return await apiRequest(
		`/vm/${rid}/actions/${action}`,
		VMActionResponseSchema,
		'POST',
		undefined,
		{
			hostname
		}
	);
}

export interface CaptureVMTemplateRequest {
	name: string;
}

export async function captureVMTemplate(
	rid: number,
	data: CaptureVMTemplateRequest,
	hostname?: string
): Promise<VMTemplateCaptureTaskResponse | APIResponse> {
	return await apiRequest(
		`/vm/${rid}/templates`,
		VMTemplateCaptureTaskResponseSchema,
		'POST',
		data,
		{
			hostname
		}
	);
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
): Promise<VMTemplateInstantiationTaskResponse | APIResponse> {
	return await apiRequest(
		`/vm/templates/${templateId}/vms`,
		VMTemplateInstantiationTaskResponseSchema,
		'POST',
		data,
		{ hostname }
	);
}

export async function deleteVMTemplate(
	templateId: number,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequest(`/vm/templates/${templateId}`, APIResponseSchema, 'DELETE', undefined, {
		hostname
	});
}

export async function getStats(
	rid: number,
	step: GFSStep,
	options?: NodeAPIRequestOptions
): Promise<VMStat[] | APIResponse> {
	return await apiRequest(`/vm/${rid}/stats/${step}`, z.array(VMStatSchema), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getStatsBootstrap(
	rid: number,
	options?: NodeAPIRequestOptions
): Promise<VMStatsBootstrap | APIResponse> {
	return await apiRequest(`/vm/${rid}/stats`, VMStatsBootstrapSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getVMLogs(
	rid: number,
	options?: NodeAPIRequestOptions
): Promise<VMLogs | APIResponse> {
	return await apiRequest(`/vm/${rid}/logs`, VMLogsSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function updateDescription(
	rid: number,
	description: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/description`,
		APIResponseSchema,
		'PATCH',
		{ description },
		{
			hostname
		}
	);
}

export async function updateName(
	rid: number,
	name: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequest(`/vm/${rid}/name`, APIResponseSchema, 'PATCH', { name }, { hostname });
}

export async function modifyWoL(
	rid: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/wol`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyIgnoreUMSR(
	rid: number,
	ignore: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/ignore-umsrs`,
		APIResponseSchema,
		'PUT',
		{ ignoreUMSRs: ignore },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyQemuGuestAgent(
	rid: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/qemu-guest-agent`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyTPM(
	rid: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/tpm`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyBootOrder(
	rid: number,
	startAtBoot: boolean,
	bootOrder: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/boot-order`,
		APIResponseSchema,
		'PUT',
		{ startAtBoot, bootOrder },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyClockOffset(
	rid: number,
	timeOffset: 'localtime' | 'utc',
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/clock`,
		APIResponseSchema,
		'PUT',
		{ timeOffset },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyBootRom(
	rid: number,
	bootRom: 'uefi' | 'uboot' | 'none',
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/boot-rom`,
		APIResponseSchema,
		'PUT',
		{ bootRom },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifySerialConsole(
	rid: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/serial-console`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyShutdownWaitTime(
	rid: number,
	waitTime: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/shutdown-wait-time`,
		APIResponseSchema,
		'PUT',
		{ waitTime },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyCloudInitData(
	rid: number,
	data: string,
	metadata: string,
	networkConfig: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/cloud-init`,
		APIResponseSchema,
		'PUT',
		{ data, metadata, networkConfig },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyExtraBhyveOptions(
	rid: number,
	extraBhyveOptions: string[],
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/options/extra-bhyve-options`,
		APIResponseSchema,
		'PUT',
		{ extraBhyveOptions },
		{ ...options, preserveErrors: true }
	);
}

export async function getQGAInfo(
	rid: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse | QGAInfo> {
	return await apiRequest(`/vm/${rid}/guest-agent`, QGAInfoSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}
