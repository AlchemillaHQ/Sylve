import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { CloudInitTemplateSchema, type CloudInitTemplate } from '$lib/types/utilities/cloud-init';
import { apiRequest } from '$lib/utils/http';
import z from 'zod/v4';

export async function getTemplates(): Promise<CloudInitTemplate[]> {
    return await apiRequest('/utilities/cloud-init/templates', z.array(CloudInitTemplateSchema), 'GET');
}

export async function createTemplate(data: Partial<CloudInitTemplate>): Promise<APIResponse> {
    return await apiRequest('/utilities/cloud-init/templates', APIResponseSchema, 'POST', data);
}

export async function updateTemplate(data: Partial<CloudInitTemplate>): Promise<APIResponse> {
    return await apiRequest(`/utilities/cloud-init/templates/${data.id}`, APIResponseSchema, 'PUT', data);
}

export async function deleteTemplate(id: number): Promise<APIResponse> {
    return await apiRequest(`/utilities/cloud-init/templates/${id}`, APIResponseSchema, 'DELETE');
}