import { goto } from '$app/navigation';
import { page } from '$app/state';

let _navigating = $state(false);
let _pendingHref: string | null = null;

export function useSafeGoto(href: string, opts?: Parameters<typeof goto>[1]) {
    if (page.url.pathname === href) {
        return;
    }

    if (_navigating && _pendingHref === href) {
        return;
    }

    _navigating = true;
    _pendingHref = href;

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
            _navigating = false;
            _pendingHref = null;
        });
}