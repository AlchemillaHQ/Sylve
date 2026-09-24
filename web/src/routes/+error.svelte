<script lang="ts">
	import { page } from '$app/state';
	import DetailBlock from '$lib/components/custom/Dialog/DetailBlock.svelte';
	import Button from '$lib/components/ui/button/button.svelte';

	const genericMessages = new Set([
		'Not Found',
		'Internal Error',
		'Internal Server Error',
		'Invalid response data',
		'Invalid response format',
		'The server response data did not match the expected format.',
		'The server response did not match the expected API format.',
		'Request failed',
		'The request could not be completed.'
	]);

	let message = $derived(page.error?.message?.trim() || '');
	let status = $derived(page.status ?? 500);
	let notFound = $derived(status === 404 || message === 'Not Found');
	let readable = $derived(message !== '' && !genericMessages.has(message) && /\s/.test(message));
	let headline = $derived(
		readable
			? message
			: notFound
				? 'This page does not exist.'
				: 'Something went wrong while loading this page.'
	);
	let hint = $derived(
		readable ? '' : notFound ? 'Use the sidebar to get back to a node.' : 'Try again in a moment.'
	);
	let icon = $derived(
		notFound ? 'icon-[mdi--file-question-outline]' : 'icon-[mdi--alert-circle-outline]'
	);
	let showDetails = $state(false);
</script>

<div
	class="flex h-full w-full flex-col items-center justify-center space-y-3 p-6 text-center text-base"
>
	<span class="{icon} text-muted-foreground h-14 w-14"></span>
	<div class="max-w-md space-y-1">
		<p class="font-medium">{headline}</p>
		{#if hint}
			<p class="text-muted-foreground text-sm">{hint}</p>
		{/if}
	</div>
	<div class="flex items-center gap-2">
		{#if !notFound}
			<Button size="sm" onclick={() => window.location.reload()}>
				<span class="icon-[mdi--refresh] h-4 w-4"></span>
				Retry
			</Button>
		{/if}
		<Button size="sm" variant="outline" onclick={() => (showDetails = !showDetails)}>
			<span class="icon-[mdi--information-outline] h-4 w-4"></span>
			{showDetails ? 'Hide details' : 'Details'}
		</Button>
	</div>
	{#if showDetails}
		<DetailBlock
			label="Error Details"
			value={{ httpStatus: status, path: page.url.pathname, message }}
			class="w-full max-w-md text-left"
		/>
	{/if}
</div>
