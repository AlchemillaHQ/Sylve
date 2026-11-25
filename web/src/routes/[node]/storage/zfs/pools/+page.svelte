<script lang="ts">
	import { page } from '$app/state';
	import ClassicPools from '$lib/components/custom/ZFS/pools/Pools.svelte';
	import Pools from '$lib/components/zen/Storage/Pools/Pools.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#if data.denied}
		<div
			class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-6 text-center"
		>
			<span class="icon-[mdi--account-lock-open-outline] size-8" aria-hidden="true"></span>
			<p class="text-foreground font-medium">Storage Pools Requires Host Access</p>
			<p class="text-sm">You do not have permission to read storage on this node.</p>
		</div>
	{:else}
		{#key data.hostname}
			<Pools
				{data}
				selectedGUID={page.url.searchParams.get('pool') ?? ''}
				create={page.url.searchParams.has('create')}
			/>
		{/key}
	{/if}
{:else}
	<ClassicPools
		data={{
			pools: Array.isArray(data.pools) ? data.pools : [],
			disks: Array.isArray(data.disks) ? data.disks : []
		}}
	/>
{/if}
