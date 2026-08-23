<script lang="ts">
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { onMount } from 'svelte';

	onMount(() => {
		useSafeGoto('/datacenter/summary', { replaceState: true });
	});
</script>
