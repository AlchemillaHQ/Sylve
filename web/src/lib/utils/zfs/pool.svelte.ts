import type { APIResponse } from '$lib/types/common';
import { getAPIErrorMessages } from '$lib/utils/http';

function prettifyError(raw: string): string {
	const idx = raw.indexOf('stderr: ');
	if (idx !== -1) {
		let msg = raw.slice(idx + 8);
		msg = msg.replace(/^\(?\s*|\s*\)?\s*$/g, '');
		return msg.trim().replace(/./, (c) => c.toUpperCase());
	}
	const msg = raw.replace(/^zpool_\w+_failed:\s*/, '');
	return msg.replace(/:\s*exit status \d+/, '').trim();
}

export function parsePoolActionError(error: APIResponse): string {
	const errorText = getAPIErrorMessages(error).join(', ');

	if (error.message && error.message === 'pool_create_failed') {
		if (errorText) {
			if (errorText.includes('mirror contains devices of different sizes')) {
				return 'Pool contains a mirror with devices of different sizes';
			} else if (errorText.includes('raidz contains devices of different sizes')) {
				return 'Pool contains a RAIDZ vdev with devices of different sizes';
			}
		}
		if (errorText) {
			return prettifyError(errorText);
		}
		return 'Pool creation failed';
	}

	if (error.message && error.message === 'pool_delete_failed') {
		if (errorText) {
			if (errorText.includes('pool or dataset is busy')) {
				return 'Pool is busy';
			}

			if (errorText.startsWith('pool ') && errorText.endsWith('is in use and cannot be deleted')) {
				return 'Pool is in use by a VM or Jail';
			}
		}
	}

	if (error.message && error.message === 'pool_edit_failed') {
		if (errorText) {
			if (errorText.startsWith('cannot replace') && errorText.includes('with smaller device')) {
				return 'Cannot replace with smaller device';
			}
		}

		return 'Pool edit failed';
	}

	if (errorText) {
		return prettifyError(errorText);
	}

	return prettifyError(error.message ?? 'An unknown error occurred');
}
