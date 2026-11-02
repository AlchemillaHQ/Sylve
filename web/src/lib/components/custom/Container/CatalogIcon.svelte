<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getCatalogIcon } from '$lib/api/container/catalog';
	import { IsInViewport, resource } from 'runed';

	let { source, id, name, node }: { source: string; id: string; name: string; node: string } =
		$props();

	let element = $state<HTMLElement>();
	const inViewport = new IsInViewport(() => element);
	let seen = $state(false);
	$effect(() => {
		if (inViewport.current) seen = true;
	});

	const icon = resource(
		() => (seen ? `${source}/${id}` : ''),
		async (key) => (key ? await getCatalogIcon(source, id, node) : ''),
		{ initialValue: '' }
	);
</script>

<div
	bind:this={element}
	class="bg-muted flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-md"
>
	{#if icon.current}
		<img src={icon.current} alt="" class="h-9 w-9 object-contain" />
	{:else}
		<span class="text-muted-foreground text-lg font-semibold">{name.charAt(0)}</span>
	{/if}
</div>
