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

	let logsContainer: HTMLDivElement | null = null;

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
		class="overflow-hidden sm:max-w-[425px]"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<Dialog.Header class="flex w-full min-w-0 flex-col items-center justify-center text-center">
			<Dialog.Title class="mb-2 text-lg font-semibold">{title}</Dialog.Title>

			{#if logs}
				<Card.Root class="w-full min-w-0 gap-0 bg-black p-4 dark:bg-black">
					<Card.Content class="mt-3 w-full max-w-full min-w-0 p-0">
						<!-- Make THIS the scroll container so we can bind to a real DOM node -->
						<div
							class="logs-container max-h-64 w-full overflow-x-auto overflow-y-auto"
							bind:this={logsContainer}
						>
							<pre class="block min-w-0 text-xs whitespace-pre text-[#4AF626]">
{logs}
							</pre>
						</div>
					</Card.Content>
				</Card.Root>
			{:else}
				<span class="icon-[mdi--loading] mb-4 animate-spin text-4xl {iconColor}"></span>
			{/if}

			<Dialog.Description class="text-muted-foreground mt-1 text-sm">
				{@html description}
			</Dialog.Description>
		</Dialog.Header>
	</Dialog.Content>
</Dialog.Root>
