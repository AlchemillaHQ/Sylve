import { storage } from '$lib';
import { getUserPreferences } from '$lib/api/auth';
import { isAPIResponse } from '$lib/utils/http';
import { toast } from 'svelte-sonner';

export interface GuestActionConfirmation {
	guestType: 'vm' | 'jail';
	guestId: number;
	name: string;
	hostname: string;
	action: 'start' | 'stop' | 'force-stop' | 'shutdown' | 'reboot' | 'restart';
}

export const guestActionConfirmation = $state({
	pending: null as GuestActionConfirmation | null
});

let resolvePending: ((confirmed: boolean) => void) | null = null;
let requesting = false;
let navigationVersion = 0;

export function finishGuestActionConfirmation(confirmed: boolean) {
	const resolve = resolvePending;
	resolvePending = null;
	guestActionConfirmation.pending = null;
	resolve?.(confirmed);
}

export function cancelGuestActionConfirmation() {
	navigationVersion += 1;
	finishGuestActionConfirmation(false);
}

export async function confirmGuestAction(request: GuestActionConfirmation): Promise<boolean> {
	if (request.action === 'start') return true;
	if (requesting) return false;
	requesting = true;
	const version = navigationVersion;
	const token = storage.token;
	try {
		const preferences = await getUserPreferences();
		if (version !== navigationVersion || token !== storage.token) return false;
		if (isAPIResponse(preferences)) {
			toast.error('Unable to load user preferences. Please try again.', {
				position: 'bottom-center'
			});
			return false;
		}
		if (!preferences.confirmGuestActions) return true;
		const confirmed = await new Promise<boolean>((resolve) => {
			resolvePending = resolve;
			guestActionConfirmation.pending = { ...request };
		});
		return confirmed && version === navigationVersion && token === storage.token;
	} finally {
		requesting = false;
	}
}
