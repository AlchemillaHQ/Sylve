/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import {
	InitializeResponseSchema,
	type InitializeResponse,
	BasicSettingsSchema,
	type BasicSettings
} from '$lib/types/basic';
import { apiRequest } from '$lib/utils/http';

export async function initialize(pools: string[], services: string[]): Promise<InitializeResponse> {
	const response = await apiRequest(
		'/basic/initialize',
		InitializeResponseSchema,
		'POST',
		{
			pools,
			services
		},
		{
			raw: true
		}
	);
	const parsed = InitializeResponseSchema.safeParse(response);

	if (parsed.success) {
		return parsed.data;
	}

	return {
		status: 'error',
		message: 'Invalid initialization response',
		error: 'The server response did not match the expected initialization format.'
	};
}

export async function getBasicSettings(hostname?: string): Promise<BasicSettings> {
	return await apiRequest('/basic/settings', BasicSettingsSchema, 'GET', undefined, { hostname });
}
