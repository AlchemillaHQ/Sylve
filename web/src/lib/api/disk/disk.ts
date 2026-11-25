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
	DiskSchema,
	SmartSelfTestDetailsSchema,
	SmartSelfTestScheduleSchema,
	type Disk,
	type SmartSelfTestDetails,
	type SmartSelfTestSchedule,
	type SmartSelfTestScheduleInput,
	type SmartSelfTestStartKind
} from '$lib/types/disk/disk';
import { apiRequest, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

function normalizeDiskAPIResult<T>(response: unknown, schema: z.ZodType<T>): T | APIResponse {
	const parsed = schema.safeParse(response);
	if (parsed.success) return parsed.data;

	const envelope = APIResponseSchema.safeParse(response);
	if (envelope.success) {
		return {
			...envelope.data,
			status: 'error',
			message: envelope.data.message || 'invalid_disk_api_response'
		};
	}

	return {
		status: 'error',
		message: 'invalid_disk_api_response'
	};
}

export async function listDisks(smart: 'full' | 'none' = 'full'): Promise<Disk[]> {
	const endpoint = smart === 'none' ? '/disk?smart=none' : '/disk';
	const response = await apiRequest(endpoint, z.array(DiskSchema), 'GET');
	return Array.isArray(response) ? response : [];
}

export async function listDisksResult(
	smart: 'full' | 'none' = 'full',
	options?: NodeAPIDataRequestOptions
): Promise<Disk[] | APIResponse> {
	const endpoint = smart === 'none' ? '/disk?smart=none' : '/disk';
	return await apiRequestResult(endpoint, z.array(DiskSchema), 'GET', undefined, options);
}

export async function getSmartSelfTest(
	device: string,
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestDetails | APIResponse> {
	const query = new URLSearchParams({ device });
	const response = await apiRequest(
		`/disk/smart/self-test?${query.toString()}`,
		SmartSelfTestDetailsSchema,
		'GET',
		undefined,
		options
	);
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function startSmartSelfTest(
	device: string,
	testType: SmartSelfTestStartKind,
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestDetails | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test',
		SmartSelfTestDetailsSchema,
		'POST',
		{ device, testType },
		options
	);
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function abortSmartSelfTest(
	device: string,
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestDetails | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/abort',
		SmartSelfTestDetailsSchema,
		'POST',
		{ device },
		options
	);
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function listSmartSelfTestSchedules(
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestSchedule[] | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/schedules',
		APIResponseSchema,
		'GET',
		undefined,
		{ ...options, raw: true }
	);
	const envelope = APIResponseSchema.safeParse(response);
	if (!envelope.success) {
		return { status: 'error', message: 'invalid_disk_api_response' };
	}
	if (envelope.data.status === 'error') return envelope.data;
	const schedules = z.array(SmartSelfTestScheduleSchema).safeParse(envelope.data.data);
	if (schedules.success) return schedules.data;
	return { status: 'error', message: 'invalid_disk_api_response' };
}

export async function createSmartSelfTestSchedule(
	input: SmartSelfTestScheduleInput,
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestSchedule | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/schedules',
		SmartSelfTestScheduleSchema,
		'POST',
		input,
		options
	);
	return normalizeDiskAPIResult(response, SmartSelfTestScheduleSchema);
}

export async function updateSmartSelfTestSchedule(
	id: number,
	input: SmartSelfTestScheduleInput,
	options?: NodeAPIDataRequestOptions
): Promise<SmartSelfTestSchedule | APIResponse> {
	const response = await apiRequest(
		`/disk/smart/self-test/schedules/${id}`,
		SmartSelfTestScheduleSchema,
		'PUT',
		input,
		options
	);
	return normalizeDiskAPIResult(response, SmartSelfTestScheduleSchema);
}

export async function deleteSmartSelfTestSchedule(
	id: number,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/disk/smart/self-test/schedules/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

function diskPathSegment(device: string): string {
	return encodeURIComponent(device.replace(/^\/dev\//, ''));
}

export async function destroyPartition(
	partition: string,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/disk/partitions/${diskPathSegment(partition)}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function initializeGPT(
	disk: string,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/disk/${diskPathSegment(disk)}/partition-table`,
		APIResponseSchema,
		'POST',
		undefined,
		options
	);
}

export async function clearPartitionTable(
	disk: string,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/disk/${diskPathSegment(disk)}/partition-table`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function createPartitions(
	disk: string,
	sizes: number[],
	types?: string[],
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/disk/${diskPathSegment(disk)}/partitions`,
		APIResponseSchema,
		'POST',
		{ sizes, ...(types ? { types } : {}) },
		options
	);
}
