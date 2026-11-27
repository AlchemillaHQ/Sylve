/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';

export const SambaAuditLogSchema = z.object({
	id: z.number(),
	shareId: z.number().default(0),
	share: z.string().default(''),
	user: z.string().default(''),
	client: z.string().default(''),
	ip: z.string().default(''),
	action: z.string(),
	result: z.string().default(''),
	path: z.string().default(''),
	target: z.string().default(''),
	folder: z.string().default(''),
	occurrences: z.number().default(1),
	createdAt: z.string()
});

export type SambaAuditLog = z.infer<typeof SambaAuditLogSchema>;

export const SambaAuditLogPageSchema = z.object({
	last_page: z.number(),
	total: z.number().default(0),
	data: SambaAuditLogSchema.array()
});

export type SambaAuditLogPage = z.infer<typeof SambaAuditLogPageSchema>;

export type SambaAuditLogQuery = {
	page: number;
	size: number;
	sortField: string;
	sortDir: 'asc' | 'desc';
	share: string;
	user: string;
	actions: string[];
	search: string;
	from?: string;
	to?: string;
};
