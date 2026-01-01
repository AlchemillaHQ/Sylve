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
	InitializeSchema,
	type Initialize,
	BasicSettingsSchema,
	type BasicSettings
} from '$lib/types/basic';
import { apiRequest } from '$lib/utils/http';

export async function initialize(pools: string[], services: string[]): Promise<Initialize> {
	return await apiRequest('/basic/initialize', InitializeSchema, 'POST', {
		pools,
		services
	});
}

export async function getBasicSettings(): Promise<BasicSettings> {
	return await apiRequest('/basic/settings', BasicSettingsSchema, 'GET');
}
