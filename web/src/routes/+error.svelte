<script lang="ts">
	import { page } from '$app/state';

	let message = $derived(page.error?.message || '');
</script>

{#if message === 'Not Found'}
	<div class="absolute inset-0 grid place-items-center">
		<span class="text-3xl text-muted">{message}</span>
	</div>
{/if}
