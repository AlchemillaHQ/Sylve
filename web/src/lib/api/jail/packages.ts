import { apiRequestResult, type NodeAPIRequestOptions } from '$lib/utils/http';
import {
	PackageDetailsSchema,
	PackageJobSchema,
	PackageLogSchema,
	PackageMonitoringSchema,
	PackageViewSchema,
	type PackageAction,
	type PackageRequest
} from '$lib/types/jail/packages';

export const getJailPackages = (ctId: number, options: NodeAPIRequestOptions = {}) =>
	apiRequestResult(`/jail/${ctId}/packages`, PackageViewSchema, 'GET', undefined, options);
export const getJailPackageDetails = (
	ctId: number,
	name: string,
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		`/jail/${ctId}/packages/installed/${encodeURIComponent(name)}`,
		PackageDetailsSchema,
		'GET',
		undefined,
		options
	);
export const startPackageOperation = (
	ctId: number,
	action: PackageAction,
	request: PackageRequest = {},
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(`/jail/${ctId}/packages/${action}`, PackageJobSchema, 'POST', request, options);
export const getPackageJob = (ctId: number, taskId: number, options: NodeAPIRequestOptions = {}) =>
	apiRequestResult(
		`/jail/${ctId}/packages/jobs/${taskId}`,
		PackageJobSchema,
		'GET',
		undefined,
		options
	);
export const getPackageLogs = (
	ctId: number,
	taskId: number,
	offset: number,
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		`/jail/${ctId}/packages/jobs/${taskId}/logs?offset=${offset}`,
		PackageLogSchema,
		'GET',
		undefined,
		options
	);
export const setPackageMonitoring = (
	ctId: number,
	enabled: boolean,
	intervalHours: number,
	options: NodeAPIRequestOptions = {}
) =>
	apiRequestResult(
		`/jail/${ctId}/packages/monitoring`,
		PackageMonitoringSchema,
		'PUT',
		{ enabled, intervalHours },
		options
	);
