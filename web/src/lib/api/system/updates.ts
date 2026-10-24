import { z } from 'zod/v4';
import { apiRequestResult, type NodeAPIRequestOptions } from '$lib/utils/http';
import {
	HostUpdatesSchema,
	HostUpdateJobSchema,
	HostUpdateMonitoringSchema,
	SylveReleaseSchema,
	type HostUpdateAction,
	type HostUpdateRequest
} from '$lib/types/system/updates';

export const getHostUpdates = (options: NodeAPIRequestOptions = {}) =>
	apiRequestResult('/system/updates', HostUpdatesSchema, 'GET', undefined, options);
export const getSylveRelease = (refresh = false, options: NodeAPIRequestOptions = {}) =>
	apiRequestResult(
		`/system/updates/sylve/release${refresh ? '?refresh=true' : ''}`,
		SylveReleaseSchema,
		'GET',
		undefined,
		options
	);
export const startHostUpdate = (
	action: HostUpdateAction,
	request: HostUpdateRequest = {},
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		`/system/updates/actions/${action}`,
		HostUpdateJobSchema,
		'POST',
		request,
		options
	);
export const getHostUpdateJob = (id: number, options: NodeAPIRequestOptions = {}) =>
	apiRequestResult(`/system/updates/jobs/${id}`, HostUpdateJobSchema, 'GET', undefined, options);
export const getHostUpdateLogs = (
	id: number,
	offset: number,
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		`/system/updates/jobs/${id}/logs?offset=${offset}`,
		z.object({ text: z.string(), next: z.number() }),
		'GET',
		undefined,
		options
	);
export const setHostUpdateMonitoring = (
	enabled: boolean,
	intervalHours: number,
	cronExpr = '',
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		'/system/updates/monitoring',
		HostUpdateMonitoringSchema,
		'PUT',
		{ enabled, intervalHours, cronExpr },
		options
	);
export const setSylveReleaseChannel = (
	channel: 'rolling' | 'tagged',
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		'/system/updates/sylve/channel',
		HostUpdateMonitoringSchema,
		'PUT',
		{ channel },
		options
	);
