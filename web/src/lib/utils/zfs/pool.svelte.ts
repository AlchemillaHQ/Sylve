import type { APIResponse } from '$lib/types/common';

export function parsePoolActionError(error: APIResponse): string {
	if (error.message && error.message === 'pool_create_failed') {
		if (error.error) {
			if (error.error.includes('mirror contains devices of different sizes')) {
				return 'Pool contains a mirror with devices of different sizes';
			} else if (error.error.includes('raidz contains devices of different sizes')) {
				return 'Pool contains a RAIDZ vdev with devices of different sizes';
			}
		}
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

	return '';
}
