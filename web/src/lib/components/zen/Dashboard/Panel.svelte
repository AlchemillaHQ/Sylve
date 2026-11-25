<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		title: string;
		icon: string;
		class?: string;
		aside?: Snippet;
		children: Snippet;
	}

	let { title, icon, class: className = '', aside, children }: Props = $props();
</script>

<section class="bg-card flex min-w-0 flex-col rounded-lg border p-4 {className}">
	<header class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
		<span class="{icon} text-muted-foreground size-4 shrink-0"></span>
		<h2 class="font-semibold">{title}</h2>
		{#if aside}
			<div class="text-muted-foreground ml-auto text-xs">{@render aside()}</div>
		{/if}
	</header>
	<div class="mt-3 flex min-h-0 min-w-0 flex-1 flex-col">{@render children()}</div>
</section>
