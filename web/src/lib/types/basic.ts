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

export const InitializeSchema = z.array(z.string());

export const BasicSettingsSchema = z.object({
	pools: z.array(z.string()),
	services: z.array(z.string()),
	initialized: z.boolean()
});

export type Initialize = z.infer<typeof InitializeSchema>;
export type BasicSettings = z.infer<typeof BasicSettingsSchema>;
