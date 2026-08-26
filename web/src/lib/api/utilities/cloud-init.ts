import type { APIResponse } from '$lib/types/common';
import {
	CloudInitTemplateIdentitySchema,
	CloudInitTemplateSchema,
	type CloudInitTemplate,
	type CloudInitTemplateIdentity,
	type CloudInitTemplateInput
} from '$lib/types/utilities/cloud-init';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import z from 'zod/v4';

export async function getTemplates(
	options?: NodeAPIRequestOptions
): Promise<CloudInitTemplate[] | APIResponse> {
	return await apiRequest(
		'/utilities/cloud-init/templates',
		z.array(CloudInitTemplateSchema),
		'GET',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function createTemplate(
	data: CloudInitTemplateInput,
	options?: NodeAPIRequestOptions
): Promise<CloudInitTemplate | APIResponse> {
	return await apiRequest(
		'/utilities/cloud-init/templates',
		CloudInitTemplateSchema,
		'POST',
		data,
		{ ...options, preserveErrors: true }
	);
}

export async function updateTemplate(
	id: number,
	data: CloudInitTemplateInput,
	options?: NodeAPIRequestOptions
): Promise<CloudInitTemplate | APIResponse> {
	return await apiRequest(
		`/utilities/cloud-init/templates/${id}`,
		CloudInitTemplateSchema,
		'PUT',
		data,
		{ ...options, preserveErrors: true }
	);
}

export async function deleteTemplate(
	id: number,
	options?: NodeAPIRequestOptions
): Promise<CloudInitTemplateIdentity | APIResponse> {
	return await apiRequest(
		`/utilities/cloud-init/templates/${id}`,
		CloudInitTemplateIdentitySchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}
