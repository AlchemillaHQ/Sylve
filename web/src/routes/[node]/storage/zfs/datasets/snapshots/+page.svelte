<script lang="ts">
	import ClassicSnapshots from '$lib/components/custom/ZFS/datasets/snapshots/Snapshots.svelte';
	import Snapshots from '$lib/components/zen/Storage/Snapshots/Snapshots.svelte';
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
			<p class="text-foreground font-medium">Snapshots Requires Host Access</p>
			<p class="text-sm">You do not have permission to read storage on this node.</p>
		</div>
	{:else}
		{#key data.hostname}
			<Snapshots {data} />
		{/key}
	{/if}
{:else}
	<ClassicSnapshots
		data={{
			hostname: data.hostname,
			basicSettings: data.basicSettings,
			periodicSnapshots: Array.isArray(data.periodicSnapshots) ? data.periodicSnapshots : []
		}}
	/>
{/if}
