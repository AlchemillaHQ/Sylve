/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';
import { APIResponseSchema } from '$lib/types/common';

export const InitializeResponseSchema = APIResponseSchema.extend({
	data: z.array(z.string()).nullable().optional()
});

export type InitializeResponse = z.infer<typeof InitializeResponseSchema>;

export const InitializeHostStateSchema = z.object({
	pf: z.object({
		conf: z.enum(['missing', 'managed', 'unmanaged']),
		backupExists: z.boolean(),
		running: z.boolean(),
		rules: z.number(),
		natRules: z.number(),
		enabledInRcConf: z.boolean()
	}),
	dhcpConfigCustom: z.boolean(),
	racctLoaderChange: z.boolean()
});

export type InitializeHostState = z.infer<typeof InitializeHostStateSchema>;
