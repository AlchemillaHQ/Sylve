<script lang="ts">
	import ClassicVolumes from '$lib/components/custom/ZFS/datasets/volumes/Volumes.svelte';
	import Volumes from '$lib/components/zen/Storage/Volumes/Volumes.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#if data.denied || !allowed('Sys.Audit', nodePath(data.hostname))}
		<div
			class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-6 text-center"
		>
			<span class="icon-[mdi--account-lock-open-outline] size-8" aria-hidden="true"></span>
			<p class="text-foreground font-medium">Volumes Requires Host Access</p>
			<p class="text-sm">You do not have permission to read storage on this node.</p>
		</div>
	{:else}
		{#key data.hostname}
			<Volumes {data} />
		{/key}
	{/if}
{:else}
	<ClassicVolumes
		data={{
			node: data.node,
			datasets: Array.isArray(data.datasets) ? data.datasets : [],
			downloads: data.downloads,
			settings: data.settings,
			loadErrors: data.loadErrors
		}}
	/>
{/if}
