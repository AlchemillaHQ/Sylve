import { goto } from '$app/navigation';
import { page } from '$app/state';

let _navigatingHref: string | null = $state(null);

export function useSafeGoto(href: string, opts?: Parameters<typeof goto>[1]) {
    if (page.url.pathname === href) {
        return;
    }

    if (_navigatingHref === href) {
        return;
    }

    _navigatingHref = href;

    // eslint-disable-next-line svelte/no-navigation-without-resolve
    return goto(href, opts)
        .catch((err) => {
            if (
                err instanceof Error &&
                err.message === 'navigation aborted'
            ) {
                return;
            }
            throw err;
        })
        .finally(() => {
            if (_navigatingHref === href) {
                _navigatingHref = null;
            }
        });
}