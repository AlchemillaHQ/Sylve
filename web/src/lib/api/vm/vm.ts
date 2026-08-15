import {
	APIResponseSchema,
	GuestDeletionResponseSchema,
	type APIResponse,
	type GFSStep
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
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

function toExtraBhyveOptions(raw: string): string[] {
	return raw
		.split('\n')
		.map((line) => line.trim())
		.filter((line) => line.length > 0);
}

export async function getVmById(rid: number, options?: NodeAPIDataRequestOptions): Promise<VM> {
	return await apiRequestData(`/vm/${rid}`, VMSchema, 'GET', undefined, options);
}

export async function getVmByIdResult(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<VM | APIResponse> {
	return await apiRequestResult(`/vm/${rid}`, VMSchema, 'GET', undefined, options);
}

export async function getVMs(hostname?: string): Promise<VM[]> {
	return await apiRequestData('/vm', z.array(VMSchema), 'GET', undefined, { hostname });
}

export async function getVMsResult(
	options?: NodeAPIDataRequestOptions
): Promise<VM[] | APIResponse> {
	return await apiRequestResult('/vm', z.array(VMSchema), 'GET', undefined, options);
}

export async function getSimpleVMs(hostname?: string, signal?: AbortSignal): Promise<SimpleVm[]> {
	return await apiRequestData('/vm/simple', z.array(SimpleVmSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getSimpleVMTemplates(hostname?: string): Promise<SimpleVmTemplate[]> {
	return await apiRequestData('/vm/templates', z.array(SimpleVmTemplateSchema), 'GET', undefined, {
		hostname
	});
}

export async function getVMTemplateById(
	templateId: number,
	hostname?: string
): Promise<VMTemplate> {
	return await apiRequestData(`/vm/templates/${templateId}`, VMTemplateSchema, 'GET', undefined, {
		hostname
	});
}

export async function getVMTemplateByIdResult(
	templateId: number,
	hostname?: string
): Promise<VMTemplate | APIResponse> {
	return await apiRequestResult(`/vm/templates/${templateId}`, VMTemplateSchema, 'GET', undefined, {
		hostname
	});
}

export async function getSimpleVMById(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<SimpleVm> {
	return await apiRequestData(`/vm/simple/${rid}`, SimpleVmSchema, 'GET', undefined, options);
}

export async function getSimpleVMByIdResult(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<SimpleVm | APIResponse> {
	return await apiRequestResult(`/vm/simple/${rid}`, SimpleVmSchema, 'GET', undefined, options);
}

export async function newVM(data: CreateData, hostname: string): Promise<APIResponse> {
	const iso = data.storage.iso.toLowerCase() === 'none' ? '' : data.storage.iso;

	return await apiRequestResult(
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
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}?deletemacs=${deleteMacs}&deleterawdisks=${deleteRawDisks}&deletevolumes=${deleteVolumes}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function forceDeleteVM(
	rid: number,
	deleteMacs: boolean = true,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}?force=true&deletemacs=${deleteMacs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function purgeVMRegistration(
	rid: number,
	deleteMacs: boolean = true,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/registration?deletemacs=${deleteMacs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function getVMDomain(
	rid: number | string,
	options?: NodeAPIDataRequestOptions
): Promise<VMDomain | APIResponse> {
	return await apiRequestResult(`/vm/${rid}/domain`, VMDomainSchema, 'GET', undefined, options);
}

export async function actionVm(
	rid: number | string,
	action: VMLifecycleAction,
	hostname?: string
): Promise<VMActionResponse | APIResponse> {
	return await apiRequestResult(
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
	return await apiRequestResult(
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
	return await apiRequestResult(
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
	return await apiRequestResult(
		`/vm/templates/${templateId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{
			hostname
		}
	);
}

export async function getStats(
	rid: number,
	step: GFSStep,
	options?: NodeAPIDataRequestOptions
): Promise<VMStat[] | APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/stats/${step}`,
		z.array(VMStatSchema),
		'GET',
		undefined,
		options
	);
}

export async function getStatsBootstrap(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<VMStatsBootstrap | APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/stats`,
		VMStatsBootstrapSchema,
		'GET',
		undefined,
		options
	);
}

export async function getVMLogs(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<VMLogs | APIResponse> {
	return await apiRequestResult(`/vm/${rid}/logs`, VMLogsSchema, 'GET', undefined, options);
}

export async function updateDescription(
	rid: number,
	description: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
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
	return await apiRequestResult(
		`/vm/${rid}/name`,
		APIResponseSchema,
		'PATCH',
		{ name },
		{ hostname }
	);
}

export async function modifyWoL(
	rid: number,
	enabled: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/wol`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
}

export async function modifyIgnoreUMSR(
	rid: number,
	ignore: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/ignore-umsrs`,
		APIResponseSchema,
		'PUT',
		{ ignoreUMSRs: ignore },
		options
	);
}

export async function modifyQemuGuestAgent(
	rid: number,
	enabled: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/qemu-guest-agent`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
}

export async function modifyTPM(
	rid: number,
	enabled: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/tpm`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
}

export async function modifyBootOrder(
	rid: number,
	startAtBoot: boolean,
	bootOrder: number,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/boot-order`,
		APIResponseSchema,
		'PUT',
		{ startAtBoot, bootOrder },
		options
	);
}

export async function modifyClockOffset(
	rid: number,
	timeOffset: 'localtime' | 'utc',
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/clock`,
		APIResponseSchema,
		'PUT',
		{ timeOffset },
		options
	);
}

export async function modifyBootRom(
	rid: number,
	bootRom: 'uefi' | 'uboot' | 'none',
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/boot-rom`,
		APIResponseSchema,
		'PUT',
		{ bootRom },
		options
	);
}

export async function modifySerialConsole(
	rid: number,
	enabled: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/serial-console`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
}

export async function modifyShutdownWaitTime(
	rid: number,
	waitTime: number,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/shutdown-wait-time`,
		APIResponseSchema,
		'PUT',
		{ waitTime },
		options
	);
}

export async function modifyCloudInitData(
	rid: number,
	data: string,
	metadata: string,
	networkConfig: string,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/cloud-init`,
		APIResponseSchema,
		'PUT',
		{ data, metadata, networkConfig },
		options
	);
}

export async function modifyExtraBhyveOptions(
	rid: number,
	extraBhyveOptions: string[],
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/vm/${rid}/options/extra-bhyve-options`,
		APIResponseSchema,
		'PUT',
		{ extraBhyveOptions },
		options
	);
}

export async function getQGAInfo(
	rid: number,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse | QGAInfo> {
	return await apiRequestResult(`/vm/${rid}/guest-agent`, QGAInfoSchema, 'GET', undefined, options);
}
