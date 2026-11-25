<script lang="ts">
	import Summary from '$lib/components/custom/Node/Summary.svelte';
	import Dashboard from '$lib/components/zen/Dashboard/Dashboard.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#key data.hostname}
		<Dashboard {data} />
	{/key}
{:else}
	<Summary {data} />
{/if}
