import { redirect } from '@sveltejs/kit';

export function load({ url }: { url: URL }) {
	const basePath = url.pathname.replace(/\/$/, '');
	const tab = url.searchParams.get('tab') ?? url.searchParams.get('dialog');
	const section = tab === 'audit' || tab === 'history' || tab === 'sylve' ? tab : 'packages';
	const query = new URLSearchParams(url.searchParams);
	query.delete('tab');
	if (tab === 'sylve') query.delete('dialog');
	if (tab === 'monitoring') query.set('dialog', tab);
	throw redirect(307, `${basePath}/${section}${query.size ? `?${query}` : ''}`);
}
