import { z } from 'zod/v4';
import { api } from '$lib/api/common';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	CertificateDomainCheckSchema,
	CertificateSchema,
	type Certificate,
	type CertificateDomainCheck,
	type CertificateInput
} from '$lib/types/services/certificates';
import { apiRequest } from '$lib/utils/http';

export async function getCertificates(
	hostname: string,
	signal?: AbortSignal
): Promise<Certificate[] | APIResponse> {
	return await apiRequest('/certificates', z.array(CertificateSchema), 'GET', undefined, {
		preserveErrors: true,
		hostname,
		signal
	});
}

export async function createCertificate(
	input: CertificateInput,
	hostname: string
): Promise<Certificate | APIResponse> {
	return await apiRequest('/certificates', CertificateSchema, 'POST', input, { hostname });
}

export async function updateCertificate(
	id: number,
	input: CertificateInput,
	hostname: string
): Promise<Certificate | APIResponse> {
	return await apiRequest(`/certificates/${id}`, CertificateSchema, 'PATCH', input, { hostname });
}

export async function deleteCertificate(id: number, hostname: string): Promise<APIResponse> {
	return await apiRequest(`/certificates/${id}`, APIResponseSchema, 'DELETE', undefined, {
		hostname
	});
}

export async function activateCertificate(
	id: number,
	hostname: string
): Promise<Certificate | APIResponse> {
	return await apiRequest(`/certificates/${id}/activate`, CertificateSchema, 'POST', undefined, {
		hostname
	});
}

export async function cancelCertificateActivation(
	id: number,
	hostname: string
): Promise<APIResponse> {
	return await apiRequest(`/certificates/${id}/activate`, APIResponseSchema, 'DELETE', undefined, {
		hostname
	});
}

export async function renewCertificate(
	id: number,
	hostname: string
): Promise<Certificate | APIResponse> {
	return await apiRequest(`/certificates/${id}/renew`, CertificateSchema, 'POST', undefined, {
		hostname
	});
}

export async function retryCertificateIssuance(
	id: number,
	hostname: string
): Promise<Certificate | APIResponse> {
	return await apiRequest(`/certificates/${id}/retry`, CertificateSchema, 'POST', undefined, {
		hostname
	});
}

export async function downloadCertificate(
	id: number,
	hostname: string
): Promise<Blob | APIResponse> {
	const response = await api.get<unknown>(`/certificates/${id}/archive`, {
		responseType: 'blob',
		headers: { 'X-Current-Hostname': hostname }
	});
	if (response.data instanceof Blob && response.data.size > 0) return response.data;

	const apiResponse = APIResponseSchema.safeParse(response.data);
	if (apiResponse.success) return apiResponse.data;
	return {
		status: 'error',
		message: 'Invalid certificate download',
		error: 'The server did not return a certificate archive.'
	};
}

export async function checkCertificateDomain(
	domain: string,
	hostname: string,
	signal?: AbortSignal
): Promise<CertificateDomainCheck | APIResponse> {
	return await apiRequest(
		`/certificates/domain-check?domain=${encodeURIComponent(domain)}`,
		CertificateDomainCheckSchema,
		'GET',
		undefined,
		{ hostname, signal }
	);
}
