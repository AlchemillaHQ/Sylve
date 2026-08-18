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

function encodeBase64URL(input: ArrayBuffer): string;
function encodeBase64URL(input: ArrayBuffer | null): string | null;

function encodeBase64URL(input: ArrayBuffer | null): string | null {
	if (input === null) {
		return null;
	}

	const bytes = new Uint8Array(input);
	let binary = '';

	for (let i = 0; i < bytes.byteLength; i++) {
		binary += String.fromCharCode(bytes[i]);
	}

	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

function mapDescriptorIds<T extends { id: string }>(
	descriptors: T[]
): (Omit<T, 'id'> & { id: ArrayBuffer })[] {
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

type PublicKeyCredentialCreationOptionsJSON = Omit<
	PublicKeyCredentialCreationOptions,
	'challenge' | 'user' | 'excludeCredentials'
> & {
	challenge: string;
	user: Omit<PublicKeyCredentialUserEntity, 'id'> & {
		id: string;
	};
	excludeCredentials?: Array<
		Omit<PublicKeyCredentialDescriptor, 'id'> & {
			id: string;
		}
	>;
};

export function buildRegistrationOptions(
	publicKey: PublicKeyCredentialCreationOptionsJSON
): PublicKeyCredentialCreationOptions {
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

type CredentialDescriptorJSON = Omit<PublicKeyCredentialDescriptor, 'id'> & {
	id: string;
};

type PublicKeyCredentialRequestOptionsJSON = Omit<
	PublicKeyCredentialRequestOptions,
	'challenge' | 'allowCredentials'
> & {
	challenge: string;
	allowCredentials?: CredentialDescriptorJSON[];
};

export function buildLoginOptions(
	publicKey: PublicKeyCredentialRequestOptionsJSON
): PublicKeyCredentialRequestOptions {
	return {
		...publicKey,
		challenge: decodeBase64URL(publicKey.challenge),
		allowCredentials: Array.isArray(publicKey.allowCredentials)
			? mapDescriptorIds(publicKey.allowCredentials)
			: undefined
	};
}

type SerializedCredentialBase = {
	id: string;
	type: string;
	rawId: string;
	authenticatorAttachment: string;
	clientExtensionResults: AuthenticationExtensionsClientOutputs;
	response: object;
};

type SerializedAssertionCredential = Omit<SerializedCredentialBase, 'response'> & {
	response: {
		authenticatorData: string;
		clientDataJSON: string;
		signature: string;
		userHandle: string;
	};
};

type SerializedAttestationCredential = Omit<SerializedCredentialBase, 'response'> & {
	response: {
		attestationObject: string;
		clientDataJSON: string;
		transports: AuthenticatorTransport[];
	};
};

type SerializedCredential =
	| SerializedCredentialBase
	| SerializedAssertionCredential
	| SerializedAttestationCredential;

export function serializeCredential(credential: PublicKeyCredential): SerializedCredential {
	const credentialWithAttachment = credential as PublicKeyCredential & {
		authenticatorAttachment?: string | null;
	};

	const base: SerializedCredentialBase = {
		id: credential.id,
		type: credential.type,
		rawId: encodeBase64URL(credential.rawId),
		authenticatorAttachment: credentialWithAttachment.authenticatorAttachment ?? '',
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
		const responseWithTransports = credential.response as AuthenticatorAttestationResponse & {
			getTransports?: () => AuthenticatorTransport[];
		};

		const transports =
			typeof responseWithTransports.getTransports === 'function'
				? responseWithTransports.getTransports()
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
