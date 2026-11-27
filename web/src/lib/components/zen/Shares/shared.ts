/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { APIResponse } from '$lib/types/common';
import { isAPIResponse } from '$lib/utils/http';

export type DatasetChoice = { guid: string; name: string; mountpoint: string; reason: string };

export function cachedStatus<T extends object>(status: T | APIResponse | null): T | undefined {
	return status && !isAPIResponse(status) ? status : undefined;
}

export function errorCode(response: Pick<APIResponse, 'error'>): string {
	return typeof response.error === 'string' ? response.error.trim() : '';
}

export function plural(count: number, word: string): string {
	return `${count} ${word}${count === 1 ? '' : 's'}`;
}

export function lowerFirst(text: string): string {
	return text.charAt(0).toLowerCase() + text.slice(1);
}

const DATASET_BLOCKS: Record<string, string> = {
	root: 'It’s the root file system',
	system_path: 'It’s mounted on a system path',
	boot_environment: 'It’s a boot environment',
	sylve_data: 'It holds Sylve’s own data',
	contains_protected_data: 'It contains Sylve’s data or a boot environment'
};

export function datasetBlockReason(code: string | undefined): string {
	if (!code) return '';
	return DATASET_BLOCKS[code] ?? 'It can’t be shared';
}

export function blockedDatasetReason(block: string, verb: 'shared' | 'exported'): string {
	return `Its dataset can’t be ${verb}: ${lowerFirst(datasetBlockReason(block))}`;
}
