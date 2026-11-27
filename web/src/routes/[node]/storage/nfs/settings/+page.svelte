<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import ClassicSettings from '$lib/components/custom/NFS/Settings.svelte';
	import Settings from '$lib/components/zen/Shares/NFS/Settings.svelte';
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
				<p class="text-foreground font-medium">NFS Requires Host Access</p>
				<p class="text-sm">You do not have permission to read NFS on this node.</p>
			</div>
		{:else}
			{#key data.hostname}
				<Settings {data} />
			{/key}
		{/if}
	{:else}
		<ClassicSettings {data} />
	{/if}
{/if}
