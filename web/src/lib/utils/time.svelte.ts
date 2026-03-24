import { pad } from './numbers';
import { SvelteDate } from 'svelte/reactivity';

export function getDashedDate() {
    const d = new SvelteDate();
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}-${pad(d.getHours())}-${pad(d.getMinutes())}-${pad(d.getSeconds())}`;
}