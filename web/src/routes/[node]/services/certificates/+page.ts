import { getCertificates } from '$lib/api/services/certificates';
import { getDynamicDNSEntries } from '$lib/api/services/dynamic-dns';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	const [certificateResult, dynamicDNSResult] = await Promise.all([
		cachedFetch('certificates', async () => await getCertificates(hostname), SEVEN_DAYS, false, hostname),
		cachedFetch(
			'dynamic-dns-entries',
			async () => await getDynamicDNSEntries(hostname, undefined, true),
			SEVEN_DAYS,
			false,
			hostname
		)
	]);

	return {
		hostname,
		certificates: Array.isArray(certificateResult) ? certificateResult : [],
		dynamicDNSEntries: Array.isArray(dynamicDNSResult) ? dynamicDNSResult : []
	};
};
