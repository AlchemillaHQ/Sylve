<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		title: string;
		value: string;
	}

	let { open = $bindable(), title, value }: Props = $props();

	async function copyValue() {
		await navigator.clipboard.writeText(value);
		const truncated =
			value.length > 20 ? value.slice(0, 20) + '...' : value;
		toast.success(`Copied "${truncated}" to clipboard`, {
			duration: 2000,
			position: 'bottom-center'
		});
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex w-[90vw] max-w-xl flex-col p-5"
		showCloseButton={true}
		onClose={() => (open = false)}
		onInteractOutside={() => (open = false)}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--code-braces]" size="h-5 w-5" gap="gap-2" {title} />
			</Dialog.Title>
		</Dialog.Header>

		<div class="my-4 max-h-[60vh] overflow-auto">
			<pre class="whitespace-pre-wrap break-all font-mono text-sm bg-muted p-3 rounded-md">{value}</pre>
		</div>

		<Dialog.Footer class="p-0">
			<Button onclick={copyValue} size="sm">
				<div class="flex items-center">
					<span class="icon-[mdi--content-copy] mr-1 h-4 w-4"></span>
					<span>Copy</span>
				</div>
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
