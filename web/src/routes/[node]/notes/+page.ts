import { getNotes } from '$lib/api/info/notes';
import { ui } from '$lib/stores/interface.svelte';
import { NotesSchema, type Note } from '$lib/types/info/notes';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	if (ui.zen) {
		let fresh = false;
		const notes = await cachedFetch(
			'notes',
			() => {
				fresh = true;
				return getNotes({ hostname: params.node, preserveErrors: true });
			},
			SEVEN_DAYS,
			false,
			params.node,
			NotesSchema
		);
		return {
			hostname: params.node,
			notes,
			fresh
		};
	}

	const cacheDuration = SEVEN_DAYS;
	const [notes] = await Promise.all([cachedFetch('notes', async () => getNotes(), cacheDuration)]);

	return {
		hostname: params.node,
		notes: notes as Note[]
	};
};
