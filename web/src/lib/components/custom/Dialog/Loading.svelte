<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';

	interface Props {
		open: boolean;
		title: string;
		description: string;
		iconColor?: string;
		logs?: string;
	}

	let {
		open = $bindable(),
		iconColor = 'text-red-500',
		title,
		description,
		logs
	}: Props = $props();

	let logsContainer: HTMLDivElement | null = $state(null);

	function scrollToBottom() {
		if (logsContainer) {
			logsContainer.scrollTop = logsContainer.scrollHeight;
		}
	}

	$effect(() => {
		if (logs && open) {
			scrollToBottom();
		}
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="overflow-hidden sm:max-w-106.25"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		showCloseButton={false}
	>
		<Dialog.Header class="flex w-full min-w-0 flex-col items-center justify-center text-center">
			<Dialog.Title class="mb-2 text-lg font-semibold">{title}</Dialog.Title>
		</Dialog.Header>

		{#if logs}
			<Card.Root class="w-full min-w-0 gap-0 bg-black p-4 dark:bg-black">
				<Card.Content class="mt-3 w-full min-w-0 max-w-full p-0">
					<div
						class="logs-container max-h-64 w-full overflow-x-auto overflow-y-auto"
						bind:this={logsContainer}
					>
						<pre class="block min-w-0 whitespace-pre text-xs text-[#4AF626]">
							{logs}
						</pre>
					</div>
				</Card.Content>
			</Card.Root>
		{:else}
			<div class="flex w-full items-center justify-center py-3 animate-spin">
				<span class="icon-[mdi--loading] text-4xl overflow-hidden {iconColor}"></span>
			</div>
		{/if}

		<div class="text-muted-foreground mt-1 justify-center text-center text-sm">
			<!-- eslint-disable-next-line svelte/no-at-html-tags -->
			{@html description}
		</div>
	</Dialog.Content>
</Dialog.Root>
