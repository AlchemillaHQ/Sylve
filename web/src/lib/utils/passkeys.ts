/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

function normalizeBase64(input: string): string {
	let normalized = input.trim().replace(/-/g, '+').replace(/_/g, '/');
	while (normalized.length % 4 !== 0) {
		normalized += '=';
	}
	return normalized;
}

function decodeBase64URL(input: string): ArrayBuffer {
	const binary = atob(normalizeBase64(input));
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) {
		bytes[i] = binary.charCodeAt(i);
	}
	return bytes.buffer;
}

function encodeBase64URL(input: ArrayBuffer | null): string | null {
	if (!input) {
		return null;
	}

	const bytes = new Uint8Array(input);
	let binary = '';
	for (let i = 0; i < bytes.byteLength; i++) {
		binary += String.fromCharCode(bytes[i]);
	}

	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

function mapDescriptorIds<T extends { id: string }>(descriptors: T[]): (Omit<T, 'id'> & { id: ArrayBuffer })[] {
	return descriptors.map((descriptor) => ({
		...descriptor,
		id: decodeBase64URL(descriptor.id)
	}));
}

export function isPasskeySupported(): boolean {
	return (
		typeof window !== 'undefined' &&
		window.isSecureContext &&
		typeof window.PublicKeyCredential !== 'undefined' &&
		typeof navigator.credentials !== 'undefined'
	);
}

export function buildRegistrationOptions(publicKey: any): PublicKeyCredentialCreationOptions {
	return {
		...publicKey,
		challenge: decodeBase64URL(publicKey.challenge),
		user: {
			...publicKey.user,
			id: decodeBase64URL(publicKey.user.id)
		},
		excludeCredentials: Array.isArray(publicKey.excludeCredentials)
			? mapDescriptorIds(publicKey.excludeCredentials)
			: undefined
	};
}

export function buildLoginOptions(publicKey: any): PublicKeyCredentialRequestOptions {
	return {
		...publicKey,
		challenge: decodeBase64URL(publicKey.challenge),
		allowCredentials: Array.isArray(publicKey.allowCredentials)
			? mapDescriptorIds(publicKey.allowCredentials)
			: undefined
	};
}

export function serializeCredential(credential: PublicKeyCredential): any {
	const base = {
		id: credential.id,
		type: credential.type,
		rawId: encodeBase64URL(credential.rawId),
		authenticatorAttachment: (credential as any).authenticatorAttachment ?? '',
		clientExtensionResults: credential.getClientExtensionResults(),
		response: {}
	};

	if (credential.response instanceof AuthenticatorAssertionResponse) {
		return {
			...base,
			response: {
				authenticatorData: encodeBase64URL(credential.response.authenticatorData),
				clientDataJSON: encodeBase64URL(credential.response.clientDataJSON),
				signature: encodeBase64URL(credential.response.signature),
				userHandle: encodeBase64URL(credential.response.userHandle)
			}
		};
	}

	if (credential.response instanceof AuthenticatorAttestationResponse) {
		const transports =
			typeof (credential.response as any).getTransports === 'function'
				? (credential.response as any).getTransports()
				: [];

		return {
			...base,
			response: {
				attestationObject: encodeBase64URL(credential.response.attestationObject),
				clientDataJSON: encodeBase64URL(credential.response.clientDataJSON),
				transports
			}
		};
	}

	return base;
}
