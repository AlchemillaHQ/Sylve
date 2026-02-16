<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	onMount(() => {
		goto('/datacenter/backups/targets', { replaceState: true });
	});
</script>
