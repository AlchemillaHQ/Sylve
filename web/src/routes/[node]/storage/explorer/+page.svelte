<script lang="ts">
	import ClassicExplorer from '$lib/components/custom/FileExplorer/Explorer.svelte';
	import Explorer from '$lib/components/zen/Storage/Explorer/Explorer.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import { isSuperuser } from '$lib/stores/permissions.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#if data.denied || !isSuperuser()}
		<div
			class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-6 text-center"
		>
			<span class="icon-[mdi--account-lock-open-outline] size-8" aria-hidden="true"></span>
			<p class="text-foreground font-medium">Explorer Requires Administrator Access</p>
			<p class="text-sm">Only administrators can browse and change this node’s files.</p>
		</div>
	{:else}
		{#key data.node}
			<Explorer hostname={data.node} />
		{/key}
	{/if}
{:else}
	<ClassicExplorer {data} />
{/if}
