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
import { apiRequest } from '$lib/utils/http';
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

export async function getSmartSelfTest(
	device: string
): Promise<SmartSelfTestDetails | APIResponse> {
	const query = new URLSearchParams({ device });
	const response = await apiRequest(
		`/disk/smart/self-test?${query.toString()}`,
		SmartSelfTestDetailsSchema,
		'GET'
	);
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function startSmartSelfTest(
	device: string,
	testType: SmartSelfTestStartKind
): Promise<SmartSelfTestDetails | APIResponse> {
	const response = await apiRequest('/disk/smart/self-test', SmartSelfTestDetailsSchema, 'POST', {
		device,
		testType
	});
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function abortSmartSelfTest(
	device: string
): Promise<SmartSelfTestDetails | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/abort',
		SmartSelfTestDetailsSchema,
		'POST',
		{
			device
		}
	);
	return normalizeDiskAPIResult(response, SmartSelfTestDetailsSchema);
}

export async function listSmartSelfTestSchedules(): Promise<SmartSelfTestSchedule[] | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/schedules',
		APIResponseSchema,
		'GET',
		undefined,
		{ raw: true }
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
	input: SmartSelfTestScheduleInput
): Promise<SmartSelfTestSchedule | APIResponse> {
	const response = await apiRequest(
		'/disk/smart/self-test/schedules',
		SmartSelfTestScheduleSchema,
		'POST',
		input
	);
	return normalizeDiskAPIResult(response, SmartSelfTestScheduleSchema);
}

export async function updateSmartSelfTestSchedule(
	id: number,
	input: SmartSelfTestScheduleInput
): Promise<SmartSelfTestSchedule | APIResponse> {
	const response = await apiRequest(
		`/disk/smart/self-test/schedules/${id}`,
		SmartSelfTestScheduleSchema,
		'PUT',
		input
	);
	return normalizeDiskAPIResult(response, SmartSelfTestScheduleSchema);
}

export async function deleteSmartSelfTestSchedule(id: number): Promise<APIResponse> {
	return await apiRequest(`/disk/smart/self-test/schedules/${id}`, APIResponseSchema, 'DELETE');
}

function diskPathSegment(device: string): string {
	return encodeURIComponent(device.replace(/^\/dev\//, ''));
}

export async function destroyPartition(partition: string): Promise<APIResponse> {
	return await apiRequest(
		`/disk/partitions/${diskPathSegment(partition)}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function initializeGPT(disk: string): Promise<APIResponse> {
	return await apiRequest(
		`/disk/${diskPathSegment(disk)}/partition-table`,
		APIResponseSchema,
		'POST'
	);
}

export async function clearPartitionTable(disk: string): Promise<APIResponse> {
	return await apiRequest(
		`/disk/${diskPathSegment(disk)}/partition-table`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function createPartitions(disk: string, sizes: number[]): Promise<APIResponse> {
	return await apiRequest(`/disk/${diskPathSegment(disk)}/partitions`, APIResponseSchema, 'POST', {
		sizes
	});
}
