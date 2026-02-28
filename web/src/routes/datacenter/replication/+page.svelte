<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	onMount(() => {
		goto('/datacenter/replication/policies', { replaceState: true });
	});
</script>
