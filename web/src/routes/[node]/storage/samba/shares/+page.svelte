<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import ClassicShares from '$lib/components/custom/Samba/Shares.svelte';
	import Shares from '$lib/components/zen/Shares/Samba/Shares.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	$effect(() => {
		if (ui.zen !== data.zen) void invalidateAll();
	});
</script>

{#if ui.zen === data.zen}
	{#if data.zen}
		{#if data.denied || !allowed('Sys.Audit', nodePath(data.hostname))}
			<div
				class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-6 text-center"
			>
				<span class="icon-[mdi--account-lock-open-outline] size-8" aria-hidden="true"></span>
				<p class="text-foreground font-medium">Samba Requires Host Access</p>
				<p class="text-sm">You do not have permission to read Samba on this node.</p>
			</div>
		{:else}
			{#key data.hostname}
				<Shares {data} />
			{/key}
		{/if}
	{:else}
		<ClassicShares {data} />
	{/if}
{/if}
