import { getJailByCTID } from '$lib/api/jail/jail';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils.js';
import type { APIResponse } from '$lib/types/common';
import type { Jail } from '$lib/types/jail/jail';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = Number(params.ctid);
	const node = String(params.node);

	const [jailResult, switches, networkObjects] = await Promise.all([
		cachedFetch(
			`jail-${ctId}`,
			async () => getJailByCTID(ctId, { hostname: node, preserveErrors: true }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'network-switches',
			async () => await getSwitches(node),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'network-objects',
			async () => await getNetworkObjects(node),
			cacheDuration,
			false,
			node
		)
	]);
	const jailError = (isAPIResponse(jailResult) ? jailResult : null) as APIResponse | null;
	const jail = (isAPIResponse(jailResult) ? null : jailResult) as Jail | null;

	return {
		node,
		ctId: ctId,
		jail,
		jailError,
		switches: switches,
		networkObjects: networkObjects
	};
}
