import { onMount } from 'svelte';

export const useWindowFocus = () => {
	let value = $state<boolean>(true);

	const onFocus = () => (value = true);
	const onBlur = () => (value = false);

	onMount(() => {
		window.addEventListener('focus', onFocus);
		window.addEventListener('blur', onBlur);
		value = document.hasFocus();
	});

	return {
		get value() {
			return value;
		}
	};
};
