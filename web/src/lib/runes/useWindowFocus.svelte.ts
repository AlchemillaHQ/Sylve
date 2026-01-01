/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

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
