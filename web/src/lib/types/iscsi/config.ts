// SPDX-License-Identifier: BSD-2-Clause

import { z } from 'zod/v4';

export const ISCSIConfigValidationSchema = z.object({
	reasonCode: z.string().nullable(),
	lineNumber: z.number().int().positive().nullable()
});

export const ISCSIConfigSchema = ISCSIConfigValidationSchema.extend({
	extraTargetConfig: z.string(),
	applyStatus: z.enum(['checked', 'pending', 'invalid', 'disabled'])
});

export type ISCSIConfig = z.infer<typeof ISCSIConfigSchema>;
export type ISCSIConfigValidation = z.infer<typeof ISCSIConfigValidationSchema>;
