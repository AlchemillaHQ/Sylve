import { storage } from '$lib';
import { sha256 } from '$lib/utils/string';

export async function load() {
	return { hash: await sha256(storage.token || '', 1) };
}
