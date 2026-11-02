/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { APIResponseSchema, type APIResponse, type GFSStep } from '$lib/types/common';
import {
	ContainerDetailSchema,
	ContainerImageInfoSchema,
	ContainerListItemSchema,
	ContainerStatSchema,
	ContainerStatsBootstrapSchema,
	ContainerTaskSchema,
	type ContainerAction,
	type ContainerCreateRequest,
	type ContainerDetail,
	type ContainerImageInfo,
	type ContainerListItem,
	type ContainerOptions,
	type ContainerSpec,
	type ContainerStat,
	type ContainerStatsBootstrap,
	type ContainerTask
} from '$lib/types/container/container';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

function specRequest<T extends ContainerSpec>(spec: T): T {
	const args = (values: string[]) => values.map((value) => value.trim()).filter(Boolean);
	return {
		...spec,
		image: spec.image.trim(),
		command: args(spec.command),
		entrypoint: args(spec.entrypoint),
		env: spec.env.filter((env) => env.name.trim()),
		ports: spec.ports.map((port) => ({
			...port,
			hostPort: Number(port.hostPort),
			containerPort: Number(port.containerPort)
		})),
		restartRetries: Number(spec.restartRetries),
		stopTimeout: Number(spec.stopTimeout)
	};
}

export async function getContainers(
	hostname?: string,
	signal?: AbortSignal
): Promise<ContainerListItem[]> {
	return await apiRequestData('/container', z.array(ContainerListItemSchema), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getContainer(
	ocId: number,
	options?: NodeAPIDataRequestOptions
): Promise<ContainerDetail | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}`,
		ContainerDetailSchema,
		'GET',
		undefined,
		options
	);
}

export async function newContainer(
	data: ContainerCreateRequest,
	hostname?: string
): Promise<ContainerTask | APIResponse> {
	return await apiRequestResult('/container', ContainerTaskSchema, 'POST', specRequest(data), {
		hostname
	});
}

export async function updateContainer(
	ocId: number,
	spec: ContainerSpec,
	hostname?: string
): Promise<ContainerTask | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}`,
		ContainerTaskSchema,
		'PUT',
		specRequest(spec),
		{
			hostname
		}
	);
}

export async function deleteContainer(
	ocId: number,
	deleteVolumes: boolean,
	hostname?: string
): Promise<ContainerTask | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}?deleteVolumes=${deleteVolumes}`,
		ContainerTaskSchema,
		'DELETE',
		undefined,
		{ hostname }
	);
}

export async function containerAction(
	ocId: number,
	action: ContainerAction,
	hostname?: string
): Promise<ContainerTask | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}/actions/${action}`,
		ContainerTaskSchema,
		'POST',
		undefined,
		{ hostname }
	);
}

export async function updateContainerOptions(
	ocId: number,
	options: ContainerOptions,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequestResult(`/container/${ocId}/options`, APIResponseSchema, 'PATCH', options, {
		hostname
	});
}

export async function getContainerLogs(
	ocId: number,
	options?: NodeAPIDataRequestOptions
): Promise<string | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}/logs?tail=500`,
		z.string(),
		'GET',
		undefined,
		options
	);
}

export async function getStats(
	ocId: number,
	step: GFSStep,
	options?: NodeAPIDataRequestOptions
): Promise<ContainerStat[] | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}/stats/${step}`,
		z.array(ContainerStatSchema),
		'GET',
		undefined,
		options
	);
}

export async function getStatsBootstrap(
	ocId: number,
	options?: NodeAPIDataRequestOptions
): Promise<ContainerStatsBootstrap | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}/stats`,
		ContainerStatsBootstrapSchema,
		'GET',
		undefined,
		options
	);
}

export async function getContainerImageInfo(
	ocId: number,
	options?: NodeAPIDataRequestOptions
): Promise<ContainerImageInfo | APIResponse> {
	return await apiRequestResult(
		`/container/${ocId}/image`,
		ContainerImageInfoSchema,
		'GET',
		undefined,
		options
	);
}
