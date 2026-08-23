<script lang="ts">
	import { resolve } from '$app/paths';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { onMount } from 'svelte';

	onMount(() => {
		useSafeGoto(resolve('/datacenter/backups/targets'), { replaceState: true });
	});
</script>
