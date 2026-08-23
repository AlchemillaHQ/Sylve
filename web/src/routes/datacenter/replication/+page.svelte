<script lang="ts">
	import { onMount } from 'svelte';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';

	onMount(() => {
		useSafeGoto('/datacenter/replication/policies', { replaceState: true });
	});
</script>
