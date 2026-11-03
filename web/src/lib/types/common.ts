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

export const APIResponseSchema = z
	.object({
		status: z.string(),
		message: z.string().optional(),
		error: z.union([z.string(), z.array(z.string())]).optional(),
		data: z.unknown().optional()
	})
	.passthrough()
	.describe('APIResponseSchema');

export const GuestDeletionDataSchema = z.object({
	warnings: z.array(z.string()).default([]),
	retainedDatasets: z.array(z.string()).default([])
});

export const GuestDeletionResponseSchema = APIResponseSchema.extend({
	data: GuestDeletionDataSchema.nullable().optional()
});

export const GFSStepSchema = z.enum(['hourly', 'daily', 'weekly', 'monthly', 'yearly']);

export const StatsHistoryStateSchema = z.enum([
	'available',
	'never-recorded',
	'outside-supported-range'
]);

export function parseGuestDeletionData(value: unknown): GuestDeletionData {
	const parsed = GuestDeletionDataSchema.safeParse(value);
	return parsed.success ? parsed.data : { warnings: [], retainedDatasets: [] };
}

export interface SeriesData {
	name: string;
	value: number;
}

export type APIResponse = z.infer<typeof APIResponseSchema>;
export type GuestDeletionData = z.infer<typeof GuestDeletionDataSchema>;
export type Locales = 'en' | 'mal' | 'hi' | 'zh-CN' | 'de' | 'cs';
export type GFSStep = z.infer<typeof GFSStepSchema>;
