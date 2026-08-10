import {
	APIResponseSchema,
	GuestDeletionResponseSchema,
	type APIResponse,
	type GFSStep,
	type GuestDeletionResponse
} from '$lib/types/common';
import {
	JailActionResponseSchema,
	JailLogsSchema,
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
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function newJail(data: CreateData, hostname?: string): Promise<APIResponse> {
	return await apiRequest(
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
	return await apiRequest('/jail/simple', z.array(SimpleJailSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getSimpleJailTemplates(hostname?: string): Promise<SimpleJailTemplate[]> {
	return await apiRequest('/jail/templates', z.array(SimpleJailTemplateSchema), 'GET', undefined, {
		hostname
	});
}

export async function getJailTemplateById(
	templateId: number,
	hostname?: string
): Promise<JailTemplate | APIResponse> {
	return await apiRequest(`/jail/templates/${templateId}`, JailTemplateSchema, 'GET', undefined, {
		hostname
	});
}

export async function getJails(hostname?: string, signal?: AbortSignal): Promise<Jail[]> {
	return await apiRequest('/jail', z.array(JailSchema), 'GET', undefined, { hostname, signal });
}

export async function getJailByCTID(
	ctId: number,
	options?: NodeAPIRequestOptions
): Promise<Jail | APIResponse> {
	return await apiRequest(`/jail/${ctId}`, JailSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getSimpleJailByCTID(
	ctId: number,
	options?: NodeAPIRequestOptions
): Promise<SimpleJail | APIResponse> {
	return await apiRequest(`/jail/simple/${ctId}`, SimpleJailSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function deleteJail(
	ctId: number,
	deleteMacs: boolean,
	deleteRootFs: boolean,
	hostname?: string
): Promise<GuestDeletionResponse> {
	return (await apiRequest(
		`/jail/${ctId}?deletemacs=${deleteMacs}&deleterootfs=${deleteRootFs}`,
		GuestDeletionResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	)) as GuestDeletionResponse;
}

export async function getJailState(
	ctId: number,
	options?: NodeAPIRequestOptions
): Promise<JailState | APIResponse> {
	return await apiRequest(`/jail/${ctId}/state`, JailStateSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function jailAction(
	ctId: number,
	action: JailLifecycleAction,
	hostname?: string
): Promise<JailActionResponse | APIResponse> {
	return await apiRequest(
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
	return await apiRequest(
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
	return await apiRequest(
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
	return await apiRequest(`/jail/templates/${templateId}`, APIResponseSchema, 'DELETE', undefined, {
		hostname
	});
}

export async function updateDescription(
	ctId: number,
	description: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequest(
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
	return await apiRequest(`/jail/${ctId}/name`, APIResponseSchema, 'PATCH', { name }, { hostname });
}

export async function getJailLogs(
	ctId: number,
	options?: NodeAPIRequestOptions
): Promise<JailLogs | APIResponse> {
	return await apiRequest(`/jail/${ctId}/logs`, JailLogsSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getStats(
	ctId: number,
	step: GFSStep,
	options?: NodeAPIRequestOptions
): Promise<JailStat[] | APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/stats/${step}`,
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
	return await apiRequest(`/jail/${ctId}/stats`, JailStatsBootstrapSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
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
	options?: NodeAPIRequestOptions
): Promise<JailNetwork | APIResponse> {
	return await apiRequest(`/jail/${ctId}/networks`, NetworkSchema, 'POST', data, {
		...options,
		preserveErrors: true
	});
}

export async function updateNetwork(
	ctId: number,
	networkId: number,
	data: Partial<JailNetworkWriteRequest>,
	options?: NodeAPIRequestOptions
): Promise<JailNetwork | APIResponse> {
	return await apiRequest(`/jail/${ctId}/networks/${networkId}`, NetworkSchema, 'PATCH', data, {
		...options,
		preserveErrors: true
	});
}

export async function deleteNetwork(
	ctId: number,
	networkId: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/networks/${networkId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function setNetworkInheritance(
	ctId: number,
	ipv4: boolean,
	ipv6: boolean,
	options?: NodeAPIRequestOptions
): Promise<JailNetworkInheritanceResult | APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/network/inheritance`,
		JailNetworkInheritanceResultSchema,
		'PUT',
		{ ipv4, ipv6 },
		{ ...options, preserveErrors: true }
	);
}
