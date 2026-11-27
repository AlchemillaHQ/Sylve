import { z } from 'zod/v4';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	NFSConfigSchema,
	NFSDatasetSchema,
	NFSPreviewSchema,
	NFSShareSchema,
	NFSStatusSchema,
	type NFSConfig,
	type NFSDataset,
	type NFSShare,
	type NFSShareInput,
	type NFSStatus
} from '$lib/types/nfs';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

const root = '/nfs';

export const getNFSConfig = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/config`, NFSConfigSchema, 'GET', undefined, options);
export const saveNFSConfig = (input: NFSConfig, options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(`${root}/config`, APIResponseSchema, 'PUT', input, options);
export const getNFSShares = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/shares`, z.array(NFSShareSchema), 'GET', undefined, options);
export const saveNFSShare = (
	id: number | null,
	input: NFSShareInput,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		id === null ? `${root}/shares` : `${root}/shares/${id}`,
		APIResponseSchema,
		id === null ? 'POST' : 'PUT',
		input,
		options
	);
export const deleteNFSShare = (id: number, options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(`${root}/shares/${id}`, APIResponseSchema, 'DELETE', undefined, options);
export const getNFSDatasets = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/datasets`, z.array(NFSDatasetSchema), 'GET', undefined, options);
export const getNFSStatus = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/status`, NFSStatusSchema, 'GET', undefined, options);
export const setNFSShareEnabled = (
	id: number,
	enabled: boolean,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(`${root}/shares/${id}/enabled`, APIResponseSchema, 'PUT', { enabled }, options);
export const getNFSConfigResult = (
	options: NodeAPIDataRequestOptions
): Promise<NFSConfig | APIResponse> =>
	apiRequestResult(`${root}/config`, NFSConfigSchema, 'GET', undefined, options);
export const getNFSSharesResult = (
	options: NodeAPIDataRequestOptions
): Promise<NFSShare[] | APIResponse> =>
	apiRequestResult(`${root}/shares`, z.array(NFSShareSchema), 'GET', undefined, options);
export const getNFSDatasetsResult = (
	options: NodeAPIDataRequestOptions
): Promise<NFSDataset[] | APIResponse> =>
	apiRequestResult(`${root}/datasets`, z.array(NFSDatasetSchema), 'GET', undefined, options);
export const getNFSStatusResult = (
	options: NodeAPIDataRequestOptions
): Promise<NFSStatus | APIResponse> =>
	apiRequestResult(`${root}/status`, NFSStatusSchema, 'GET', undefined, options);
export const getNFSPreview = (options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(`${root}/preview`, NFSPreviewSchema, 'GET', undefined, options);
export const applyNFS = (options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(`${root}/apply`, APIResponseSchema, 'POST', { confirm: true }, options);
