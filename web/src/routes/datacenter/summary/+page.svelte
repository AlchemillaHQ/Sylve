<script lang="ts">
	import Summary from '$lib/components/custom/DataCenter/Summary.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if data.limited}
	<div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-center">
		<span class="icon-[mdi--account-lock-open-outline] text-muted-foreground h-10 w-10"></span>
		<p class="text-sm font-medium">The datacenter summary needs host access.</p>
		<p class="text-muted-foreground text-sm">
			Pick a VM, jail or container you can use from the tree on the left.
		</p>
	</div>
{:else}
	<Summary {data} />
{/if}
