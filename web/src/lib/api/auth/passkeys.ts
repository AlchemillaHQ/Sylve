import { PasskeySchema, type Passkey } from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export const PasskeyChallengeSchema = z.object({
	requestId: z.string(),
	publicKey: z.any()
});

export type PasskeyChallenge = z.infer<typeof PasskeyChallengeSchema>;

export async function beginPasskeyRegistration(
	userId: number,
	options: NodeAPIRequestOptions = {}
): Promise<PasskeyChallenge | APIResponse> {
	return await apiRequest(
		'/auth/passkeys/register/begin',
		PasskeyChallengeSchema,
		'POST',
		{ userId },
		{ ...options, preserveErrors: true }
	);
}

export async function finishPasskeyRegistration(
	requestId: string,
	credential: unknown,
	label: string,
	options: NodeAPIRequestOptions = {}
): Promise<Passkey | APIResponse> {
	return await apiRequest(
		'/auth/passkeys/register/finish',
		PasskeySchema,
		'POST',
		{ requestId, credential, label },
		{ ...options, preserveErrors: true }
	);
}

export async function listUserPasskeys(
	userId: number,
	options: NodeAPIRequestOptions = {}
): Promise<Passkey[] | APIResponse> {
	return await apiRequest(
		`/auth/users/${encodeURIComponent(String(userId))}/passkeys`,
		z.array(PasskeySchema),
		'GET',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function deleteUserPasskey(
	userId: number,
	credentialId: string,
	options: NodeAPIRequestOptions = {}
): Promise<Passkey | APIResponse> {
	return await apiRequest(
		`/auth/users/${encodeURIComponent(String(userId))}/passkeys/${encodeURIComponent(credentialId)}`,
		PasskeySchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}
