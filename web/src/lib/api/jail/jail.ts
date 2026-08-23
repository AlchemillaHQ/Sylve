import {
	APIResponseSchema,
	GuestDeletionResponseSchema,
	type APIResponse,
	type GFSStep
} from '$lib/types/common';
import {
	JailActionResponseSchema,
	JailLogsSchema,
	JailRootMountPointSchema,
	JailSchema,
	JailStateSchema,
	JailStatSchema,
	JailStatsBootstrapSchema,
	JailNetworkInheritanceResultSchema,
	NetworkSchema,
	SimpleJailSchema,
	type CreateData,
	type Jail,
	type JailActionResponse,
	type JailLifecycleAction,
	type JailLogs,
	type JailRootMountPoint,
	type JailStat,
	type JailStatsBootstrap,
	type JailNetwork,
	type JailNetworkInheritanceResult,
	type JailState,
	type SimpleJail,
	JailTemplateSchema,
	type JailTemplate,
	JailTemplateCaptureTaskResponseSchema,
	type JailTemplateCaptureTaskResponse,
	JailTemplateInstantiationTaskResponseSchema,
	type JailTemplateInstantiationTaskResponse,
	SimpleJailTemplateSchema,
	type SimpleJailTemplate
} from '$lib/types/jail/jail';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function newJail(data: CreateData, hostname?: string): Promise<APIResponse> {
	return await apiRequestResult(
		'/jail',
		APIResponseSchema,
		'POST',
		{
			name: data.name,
			hostname: data.hostname,
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
		},
		{ hostname }
	);
}

export async function getSimpleJails(
	hostname?: string,
	signal?: AbortSignal
): Promise<SimpleJail[]> {
	return await apiRequestData('/jail/simple', z.array(SimpleJailSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getSimpleJailTemplates(hostname?: string): Promise<SimpleJailTemplate[]> {
	return await apiRequestData(
		'/jail/templates',
		z.array(SimpleJailTemplateSchema),
		'GET',
		undefined,
		{ hostname }
	);
}

export async function getJailTemplateById(
	templateId: number,
	hostname?: string
): Promise<JailTemplate | APIResponse> {
	return await apiRequestResult(
		`/jail/templates/${templateId}`,
		JailTemplateSchema,
		'GET',
		undefined,
		{ hostname }
	);
}

export async function getJails(hostname?: string, signal?: AbortSignal): Promise<Jail[]> {
	return await apiRequestData('/jail', z.array(JailSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getJailsResult(
	options?: NodeAPIDataRequestOptions
): Promise<Jail[] | APIResponse> {
	return await apiRequestResult('/jail', z.array(JailSchema), 'GET', undefined, options);
}

export async function getJailByCTID(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<Jail | APIResponse> {
	return await apiRequestResult(`/jail/${ctId}`, JailSchema, 'GET', undefined, options);
}

export async function getJailRootMountPoint(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<JailRootMountPoint | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/root-mountpoint`,
		JailRootMountPointSchema,
		'GET',
		undefined,
		options
	);
}

export async function getSimpleJailByCTID(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<SimpleJail | APIResponse> {
	return await apiRequestResult(
		`/jail/simple/${ctId}`,
		SimpleJailSchema,
		'GET',
		undefined,
		options
	);
}

export async function deleteJail(
	ctId: number,
	deleteMacs: boolean,
	deleteRootFs: boolean,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}?deletemacs=${deleteMacs}&deleterootfs=${deleteRootFs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function getJailState(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<JailState | APIResponse> {
	return await apiRequestResult(`/jail/${ctId}/state`, JailStateSchema, 'GET', undefined, options);
}

export async function jailAction(
	ctId: number,
	action: JailLifecycleAction,
	hostname?: string
): Promise<JailActionResponse | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/actions/${action}`,
		JailActionResponseSchema,
		'POST',
		undefined,
		{
			hostname
		}
	);
}

export interface ConvertJailToTemplateRequest {
	name: string;
}

export async function convertJailToTemplate(
	ctId: number,
	data: ConvertJailToTemplateRequest,
	hostname?: string
): Promise<JailTemplateCaptureTaskResponse | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/templates`,
		JailTemplateCaptureTaskResponseSchema,
		'POST',
		data,
		{ hostname }
	);
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
): Promise<JailTemplateInstantiationTaskResponse | APIResponse> {
	return await apiRequestResult(
		`/jail/templates/${templateId}/jails`,
		JailTemplateInstantiationTaskResponseSchema,
		'POST',
		data,
		{ hostname }
	);
}

export async function deleteJailTemplate(
	templateId: number,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/jail/templates/${templateId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function updateDescription(
	ctId: number,
	description: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/description`,
		APIResponseSchema,
		'PATCH',
		{ description },
		{ hostname }
	);
}

export async function updateName(
	ctId: number,
	name: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/name`,
		APIResponseSchema,
		'PATCH',
		{ name },
		{ hostname }
	);
}

export async function getJailLogs(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<JailLogs | APIResponse> {
	return await apiRequestResult(`/jail/${ctId}/logs`, JailLogsSchema, 'GET', undefined, options);
}

export async function getStats(
	ctId: number,
	step: GFSStep,
	options?: NodeAPIDataRequestOptions
): Promise<JailStat[] | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/stats/${step}`,
		z.array(JailStatSchema),
		'GET',
		undefined,
		options
	);
}

export async function getStatsBootstrap(
	ctId: number,
	options?: NodeAPIDataRequestOptions
): Promise<JailStatsBootstrap | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/stats`,
		JailStatsBootstrapSchema,
		'GET',
		undefined,
		options
	);
}

export interface JailNetworkWriteRequest {
	name: string;
	switchName: string;
	macId: number;
	macRaw: string;
	ip4: number;
	ip4Raw: string;
	ip4gw: number;
	ip4gwRaw: string;
	ip6: number;
	ip6Raw: string;
	ip6gw: number;
	ip6gwRaw: string;
	dhcp: boolean;
	slaac: boolean;
	defaultGateway: boolean;
	vlan: number;
}

export async function addNetwork(
	ctId: number,
	data: JailNetworkWriteRequest,
	options?: NodeAPIDataRequestOptions
): Promise<JailNetwork | APIResponse> {
	return await apiRequestResult(`/jail/${ctId}/networks`, NetworkSchema, 'POST', data, options);
}

export async function updateNetwork(
	ctId: number,
	networkId: number,
	data: Partial<JailNetworkWriteRequest>,
	options?: NodeAPIDataRequestOptions
): Promise<JailNetwork | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/networks/${networkId}`,
		NetworkSchema,
		'PATCH',
		data,
		options
	);
}

export async function deleteNetwork(
	ctId: number,
	networkId: number,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/networks/${networkId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function setNetworkInheritance(
	ctId: number,
	ipv4: boolean,
	ipv6: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<JailNetworkInheritanceResult | APIResponse> {
	return await apiRequestResult(
		`/jail/${ctId}/network/inheritance`,
		JailNetworkInheritanceResultSchema,
		'PUT',
		{ ipv4, ipv6 },
		options
	);
}
