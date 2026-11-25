<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import { PersistedState } from 'runed';
	import type { Snippet } from 'svelte';
	import AppSidebar from './Sidebar.svelte';
	import Topbar from './Topbar.svelte';

	let { children }: { children?: Snippet } = $props();

	const expanded = new PersistedState('sylve-zen-sidebar-expanded', true);

	$effect(() => {
		document.documentElement.classList.add('zen');
		return () => document.documentElement.classList.remove('zen');
	});
</script>

<Sidebar.Provider
	class="h-svh"
	style="--sidebar-width: 15rem; --sidebar-width-icon: 3.5rem;"
	bind:open={expanded.current}
>
	<AppSidebar />
	<div class="flex min-w-0 flex-1 flex-col">
		<Topbar />
		<main class="min-h-0 flex-1 overflow-y-auto">
			{@render children?.()}
		</main>
	</div>
</Sidebar.Provider>
