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
import {
	SambaAuditLogPageSchema,
	type SambaAuditLogPage,
	type SambaAuditLogQuery
} from '$lib/types/samba/audit';
import { apiRequestResult } from '$lib/utils/http';
import type { SambaRequestOptions } from './share';

export async function getSambaAuditLogs(
	query: SambaAuditLogQuery,
	options: SambaRequestOptions
): Promise<SambaAuditLogPage | APIResponse> {
	const params = new URLSearchParams({
		page: String(query.page),
		size: String(query.size),
		'sort[0][field]': query.sortField,
		'sort[0][dir]': query.sortDir
	});
	if (query.share) params.set('share', query.share);
	if (query.user.trim()) params.set('user', query.user.trim());
	if (query.actions.length) params.set('action', query.actions.join(','));
	if (query.search.trim()) params.set('q', query.search.trim());
	if (query.from) params.set('from', query.from);
	if (query.to) params.set('to', query.to);
	return await apiRequestResult(
		`/samba/audit-logs?${params.toString()}`,
		SambaAuditLogPageSchema,
		'GET',
		undefined,
		options
	);
}
