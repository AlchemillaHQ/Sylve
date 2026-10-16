import { getJailPackages } from '$lib/api/jail/packages';
import { isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params, parent, url }) {
	const { jail } = await parent();
	if (jail.type !== 'freebsd') error(404, 'Packages are available only for FreeBSD jails');
	const node = String(params.node);
	const ctId = Number(params.ctid);
	const result = await getJailPackages(ctId, { hostname: node });
	if (
		!isAPIResponse(result) &&
		((url.searchParams.has('jailId') && Number(url.searchParams.get('jailId')) !== result.jailId) ||
			(url.searchParams.has('guid') && url.searchParams.get('guid') !== result.guid))
	)
		error(404, 'Audit belongs to a different jail incarnation');
	return {
		node,
		ctId,
		packages: isAPIResponse(result) ? null : result,
		packagesError: isAPIResponse(result) ? result : null
	};
}
