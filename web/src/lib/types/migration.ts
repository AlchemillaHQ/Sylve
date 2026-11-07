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

export const ValidateResultSchema = z.object({
	allowed: z.boolean(),
	reasons: z.array(z.string()).catch([]),
	warnings: z.array(z.string()).catch([]),
	guestPools: z.array(z.string()).catch([]),
	targetNodePools: z.array(z.string()).catch([])
});

export const MigrationTaskResponseSchema = z.object({
	taskId: z.number().int().positive(),
	guestId: z.number().int().positive(),
	outcome: z.string()
});
export type ValidateResult = z.infer<typeof ValidateResultSchema>;
export type MigrationTaskResponse = z.infer<typeof MigrationTaskResponseSchema>;
