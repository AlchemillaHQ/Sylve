/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	ContainerImageSchema,
	ContainerNetworkSchema,
	RegistryCredentialSchema,
	UsedPortSchema,
	type ContainerImage,
	type ContainerNetwork,
	type ContainerNetworkRequest,
	type RegistryCredential,
	type UsedPort
} from '$lib/types/container/container';
import { getDatasetsResult } from '$lib/api/zfs/datasets';
import { apiRequestData, apiRequestResult, isAPIResponse } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getUsedPorts(hostname: string, ocId?: number): Promise<UsedPort[]> {
	return await apiRequestData(
		`/container/ports${ocId ? `?ocid=${ocId}` : ''}`,
		z.array(UsedPortSchema),
		'GET',
		undefined,
		{ hostname }
	);
}

export async function getContainerNetworks(hostname?: string): Promise<ContainerNetwork[]> {
	return await apiRequestData(
		'/container/networks',
		z.array(ContainerNetworkSchema),
		'GET',
		undefined,
		{ hostname }
	);
}

export async function createContainerNetwork(
	data: ContainerNetworkRequest,
	hostname?: string
): Promise<ContainerNetwork | APIResponse> {
	return await apiRequestResult('/container/networks', ContainerNetworkSchema, 'POST', data, {
		hostname
	});
}

export async function deleteContainerNetwork(id: number, hostname?: string): Promise<APIResponse> {
	return await apiRequestResult(
		`/container/networks/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{
			hostname
		}
	);
}

export async function getContainerImages(
	pool: string,
	hostname?: string
): Promise<ContainerImage[]> {
	return await apiRequestData(
		`/container/images/${encodeURIComponent(pool)}`,
		z.array(ContainerImageSchema),
		'GET',
		undefined,
		{ hostname }
	);
}

export async function pullContainerImage(
	pool: string,
	image: string,
	os: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/container/images/${encodeURIComponent(pool)}/pull`,
		APIResponseSchema,
		'POST',
		{ image, os },
		{ hostname }
	);
}

export async function pruneContainerImages(pool: string, hostname?: string): Promise<APIResponse> {
	return await apiRequestResult(
		`/container/images/${encodeURIComponent(pool)}/prune`,
		APIResponseSchema,
		'POST',
		undefined,
		{ hostname }
	);
}

export async function deleteContainerImage(
	pool: string,
	id: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/container/images/${encodeURIComponent(pool)}/${encodeURIComponent(id)}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function getRegistryCredentials(hostname?: string): Promise<RegistryCredential[]> {
	return await apiRequestData(
		'/container/registry-credentials',
		z.array(RegistryCredentialSchema),
		'GET',
		undefined,
		{ hostname }
	);
}

export async function saveRegistryCredential(
	data: { registry: string; username: string; password: string },
	hostname?: string
): Promise<RegistryCredential | APIResponse> {
	return await apiRequestResult(
		'/container/registry-credentials',
		RegistryCredentialSchema,
		'PUT',
		data,
		{ hostname, sensitive: true }
	);
}

export async function deleteRegistryCredential(
	id: number,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(
		`/container/registry-credentials/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function getMountableDatasets(hostname: string): Promise<string[]> {
	const result = await getDatasetsResult('FILESYSTEM', hostname);
	if (isAPIResponse(result)) return [];
	return result.map((dataset) => dataset.name).filter((name) => !/\/sylve(\/|$)/.test(name));
}
