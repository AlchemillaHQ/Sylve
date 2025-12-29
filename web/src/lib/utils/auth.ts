import { storage } from '$lib';
import { parseJwt } from './string';

export function getUsername(): string {
	try {
		const token = storage.token;
		if (!token) return 'unknown';

		const decoded = parseJwt(token);
		return decoded.custom_claims.username;
	} catch (e) {
		return 'unknown';
	}
}
