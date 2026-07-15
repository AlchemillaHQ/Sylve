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
	.describe('APIResponseSchema');

export const GuestDeletionDataSchema = z.object({
	warnings: z.array(z.string()).default([]),
	retainedDatasets: z.array(z.string()).default([])
});

export const GuestDeletionResponseSchema = APIResponseSchema.extend({
	data: GuestDeletionDataSchema.nullable().optional()
});

export function parseGuestDeletionData(value: unknown): GuestDeletionData {
	const parsed = GuestDeletionDataSchema.safeParse(value);
	return parsed.success ? parsed.data : { warnings: [], retainedDatasets: [] };
}

export interface HistoricalBase {
	id?: number | string;
	createdAt?: string | Date;
	[key: string]: number | string | Date | undefined;
}

export interface HistoricalData {
	date: Date;
	[key: string]: number | string | Date;
}

export interface PieChartData {
	label: string;
	value: number;
	color: string;
}

export interface SeriesData {
	name: string;
	value: number;
}

export interface SeriesDataWithBaseline {
	name: string;
	baseline: number;
	value: number;
}

export type APIResponse = z.infer<typeof APIResponseSchema>;
export type GuestDeletionData = z.infer<typeof GuestDeletionDataSchema>;
export type GuestDeletionResponse = z.infer<typeof GuestDeletionResponseSchema>;
export type Locales = 'en' | 'mal' | 'hi' | 'zh-CN' | 'de' | 'cs';
export type GFSStep = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'yearly';
