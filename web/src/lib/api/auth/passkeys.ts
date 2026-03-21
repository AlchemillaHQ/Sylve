import { PasskeySchema, type Passkey } from '$lib/types/auth';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export const PasskeyChallengeSchema = z.object({
	requestId: z.string(),
	publicKey: z.any()
});

export type PasskeyChallenge = z.infer<typeof PasskeyChallengeSchema>;

export async function beginPasskeyRegistration(userId: number): Promise<APIResponse> {
	return await apiRequest(
		'/auth/passkeys/register/begin',
		APIResponseSchema,
		'POST',
		{ userId },
		{ raw: true }
	);
}

export async function finishPasskeyRegistration(
	requestId: string,
	credential: unknown,
	label?: string
): Promise<APIResponse> {
	return await apiRequest(
		'/auth/passkeys/register/finish',
		APIResponseSchema,
		'POST',
		{ requestId, credential, label: label || '' },
		{ raw: true }
	);
}

export async function listUserPasskeys(userId: number): Promise<Passkey[]> {
	return await apiRequest(`/auth/passkeys/users/${userId}`, z.array(PasskeySchema), 'GET');
}

export async function deleteUserPasskey(userId: number, credentialId: string): Promise<APIResponse> {
	return await apiRequest(
		`/auth/passkeys/users/${userId}/${encodeURIComponent(credentialId)}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ raw: true }
	);
}
