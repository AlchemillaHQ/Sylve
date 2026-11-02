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
import { ContainerSchema, list } from './container';

export const CatalogVariantSchema = z.object({
	id: z.string(),
	label: z.string(),
	default: z.boolean()
});

export const CatalogAppSchema = z.object({
	source: z.string(),
	id: z.string(),
	name: z.string(),
	category: z.string(),
	description: z.string(),
	os: z.string(),
	variants: list(CatalogVariantSchema),
	stack: z.boolean().catch(false)
});

export const CatalogVariableSchema = z.object({
	name: z.string(),
	label: z.string(),
	hint: z.string().catch(''),
	type: z.string(),
	default: z.string(),
	optional: z.boolean(),
	advanced: z.boolean(),
	volume: z.string().catch(''),
	owner: z.string().catch(''),
	protocol: z.string().catch(''),
	target: z.string().catch(''),
	values: z
		.record(z.string(), z.string())
		.nullish()
		.transform((value) => value ?? {})
});

export const CatalogChoiceSchema = z.object({
	id: z.string(),
	label: z.string(),
	doc: z.string(),
	default: z.string(),
	options: list(
		z.object({
			id: z.string(),
			label: z.string(),
			doc: z.string(),
			adds: list(z.string()),
			drops: list(z.string()),
			ask: list(CatalogVariableSchema)
		})
	)
});

export const CatalogTemplateSchema = z.object({
	app: CatalogAppSchema,
	variables: list(CatalogVariableSchema),
	choices: list(CatalogChoiceSchema),
	containers: list(z.string())
});

export const CatalogRenderedSchema = ContainerSchema.pick({
	name: true,
	description: true,
	image: true,
	env: true,
	ports: true,
	mounts: true,
	restart: true,
	stopTimeout: true,
	annotations: true,
	extraHosts: true,
	shmSize: true,
	webPort: true,
	webHttps: true,
	catalogApp: true,
	stack: true
}).extend({
	os: z.enum(['', 'freebsd', 'linux']).catch(''),
	network: z.string(),
	startAfterCreate: z.boolean().catch(true)
});

export const CatalogPlanSchema = z.object({
	stack: z.string().catch(''),
	containers: list(CatalogRenderedSchema)
});

export type CatalogApp = z.infer<typeof CatalogAppSchema>;
export type CatalogVariable = z.infer<typeof CatalogVariableSchema>;
export type CatalogTemplate = z.infer<typeof CatalogTemplateSchema>;
export type CatalogPlan = z.infer<typeof CatalogPlanSchema>;
