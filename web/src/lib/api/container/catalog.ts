/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { api } from '$lib/api/common';
import type { APIResponse } from '$lib/types/common';
import {
	CatalogAppSchema,
	CatalogPlanSchema,
	CatalogTemplateSchema,
	type CatalogApp,
	type CatalogPlan,
	type CatalogTemplate
} from '$lib/types/container/catalog';
import { ContainerTaskSchema, type ContainerCreateRequest } from '$lib/types/container/container';
import { apiRequestResult } from '$lib/utils/http';
import { z } from 'zod/v4';

const appPath = (source: string, id: string) =>
	`/container/catalog/${encodeURIComponent(source)}/${encodeURIComponent(id)}`;

export async function getCatalog(hostname: string): Promise<CatalogApp[] | APIResponse> {
	return await apiRequestResult('/container/catalog', z.array(CatalogAppSchema), 'GET', undefined, {
		hostname
	});
}

export async function getCatalogTemplate(
	source: string,
	id: string,
	hostname: string
): Promise<CatalogTemplate | APIResponse> {
	return await apiRequestResult(appPath(source, id), CatalogTemplateSchema, 'GET', undefined, {
		hostname
	});
}

export async function renderCatalogApp(
	source: string,
	id: string,
	settings: {
		variant: string;
		values: Record<string, string>;
		choices: Record<string, string>;
		stack: string;
	},
	hostname: string
): Promise<CatalogPlan | APIResponse> {
	return await apiRequestResult(
		`${appPath(source, id)}/render`,
		CatalogPlanSchema,
		'POST',
		settings,
		{
			hostname
		}
	);
}

const StackTaskSchema = ContainerTaskSchema.extend({
	containers: z.array(z.object({ ocId: z.number().int(), name: z.string() }))
});

export async function newStack(
	request: {
		stack: string;
		pool: string;
		ocId: number;
		startAtBoot: boolean;
		containers: ContainerCreateRequest[];
	},
	hostname: string
): Promise<z.infer<typeof StackTaskSchema> | APIResponse> {
	return await apiRequestResult('/container/stack', StackTaskSchema, 'POST', request, {
		hostname
	});
}

const icons = new Map<string, Promise<string>>();

export function getCatalogIcon(source: string, id: string, hostname: string): Promise<string> {
	const key = `${hostname}/${source}/${id}`;
	const path = id ? appPath(source, id) : `/container/catalog/${encodeURIComponent(source)}`;
	let icon = icons.get(key);
	if (!icon) {
		icon = api
			.get<unknown>(`${path}/icon`, {
				responseType: 'blob',
				node: hostname
			})
			.then((response) =>
				response.data instanceof Blob && response.data.size > 0
					? URL.createObjectURL(response.data)
					: ''
			)
			.catch(() => '');
		icons.set(key, icon);
	}
	return icon;
}
