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

export const BasicSettingsSchema = z.object({
	pools: z.array(z.string()),
	services: z.array(z.string()),
	initialized: z.boolean()
});

export type InitializeResponse = z.infer<typeof InitializeResponseSchema>;
export type BasicSettings = z.infer<typeof BasicSettingsSchema>;
