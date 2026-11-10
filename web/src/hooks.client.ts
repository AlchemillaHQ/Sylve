import { APIRequestError } from '$lib/utils/http';
import type { HandleClientError } from '@sveltejs/kit';

const refusals = ['permission_denied', 'superuser_required'];

// A page whose data is refused shows the lock page instead of "Internal Error".
export const handleError: HandleClientError = ({ error, message }) => {
	const code = error instanceof APIRequestError ? error.response.message : undefined;
	return { message: code && refusals.includes(code) ? code : message };
};
