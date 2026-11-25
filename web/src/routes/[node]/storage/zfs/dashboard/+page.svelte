<script lang="ts">
	import ClassicDashboard from '$lib/components/custom/ZFS/Dashboard/Dashboard.svelte';
	import Dashboard from '$lib/components/zen/Storage/Dashboard.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#key data.hostname}
		<Dashboard {data} />
	{/key}
{:else}
	<ClassicDashboard {data} />
{/if}
