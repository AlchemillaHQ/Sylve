import type { APIResponse } from '$lib/types/common';

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
	if (error.message && error.message === 'pool_create_failed') {
		if (error.error) {
			if (error.error.includes('mirror contains devices of different sizes')) {
				return 'Pool contains a mirror with devices of different sizes';
			} else if (error.error.includes('raidz contains devices of different sizes')) {
				return 'Pool contains a RAIDZ vdev with devices of different sizes';
			}
		}
		if (error.error) {
			return prettifyError(error.error);
		}
		return 'Pool creation failed';
	}

	if (error.message && error.message === 'pool_delete_failed') {
		if (error.error) {
			if (error.error.includes('pool or dataset is busy')) {
				return 'Pool is busy';
			}

			if (
				!Array.isArray(error.error) &&
				error.error.startsWith('pool ') &&
				error.error.endsWith('is in use and cannot be deleted')
			) {
				return 'Pool is in use by a VM or Jail';
			}
		}
	}

	if (error.message && error.message === 'pool_edit_failed') {
		if (error.error) {
			if (
				!Array.isArray(error.error) &&
				error.error.startsWith('cannot replace') &&
				error.error.includes('with smaller device')
			) {
				return 'Cannot replace with smaller device';
			}
		}

		return 'Pool edit failed';
	}

	if (error.error) {
		return prettifyError(error.error);
	}

	return prettifyError(error.message ?? 'An unknown error occurred');
}
