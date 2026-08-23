import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import type { ExecPhaseKey, ExecPhaseState } from '$lib/types/jail/jail';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

function jailOptionRequest(
	ctId: number,
	option: string,
	body: unknown,
	options: NodeAPIRequestOptions = {}
): Promise<APIResponse> {
	return apiRequest(`/jail/${ctId}/options/${option}`, APIResponseSchema, 'PUT', body, {
		...options,
		preserveErrors: true
	});
}

export function modifyBootOrder(
	ctId: number,
	startAtBoot: boolean,
	bootOrder: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'boot-order', { startAtBoot, bootOrder }, options);
}

export function modifyWoL(
	ctId: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'wol', { enabled }, options);
}

export function modifyExecutionTimeout(
	ctId: number,
	execTimeout: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'execution-timeout', { execTimeout }, options);
}

export function modifyFstab(
	ctId: number,
	fstab: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'fstab', { fstab }, options);
}

export function modifyResolvConf(
	ctId: number,
	resolvConf: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'resolv-conf', { resolvConf }, options);
}

export function modifyDevFSRules(
	ctId: number,
	devFSRules: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'devfs-rules', { devFSRules }, options);
}

export function modifyAdditionalOptions(
	ctId: number,
	additionalOptions: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'additional-options', { additionalOptions }, options);
}

export function modifyAllowedOptions(
	ctId: number,
	allowedOptions: string[],
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'allowed-options', { allowedOptions }, options);
}

export function modifyMetadata(
	ctId: number,
	metadata: string,
	env: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'metadata', { metadata, env }, options);
}

export function modifyLifecycleHooks(
	ctId: number,
	hooks: Record<ExecPhaseKey, ExecPhaseState>,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return jailOptionRequest(ctId, 'lifecycle-hooks', { hooks }, options);
}
