import { getSylveRelease } from '$lib/api/system/updates';
import { SylveReleaseSchema } from '$lib/types/system/updates';
import { ONLY_CACHE, SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, parent }) => {
	const { updates } = await parent();
	if (!updates) return { sylveRelease: null };
	const result = await cachedFetch(
		`sylve-release-${updates.releaseChannel}`,
		() => getSylveRelease(false, { hostname: params.node }),
		SEVEN_DAYS,
		ONLY_CACHE,
		params.node,
		SylveReleaseSchema
	);
	return {
		sylveRelease:
			result && !isAPIResponse(result) && result.releaseChannel === updates.releaseChannel
				? result
				: null
	};
};
