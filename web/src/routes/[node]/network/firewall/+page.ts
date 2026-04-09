import { redirect } from '@sveltejs/kit';

export function load({ url }: { url: URL }) {
	const basePath = url.pathname.endsWith('/') ? url.pathname.slice(0, -1) : url.pathname;
	throw redirect(307, `${basePath}/traffic`);
}
