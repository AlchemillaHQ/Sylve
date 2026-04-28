<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Props {
		isOpen: boolean;
		isFolder: boolean;
		name: string;
		onClose: () => void;
		onReset: () => void;
		onCreate: () => void;
	}

	let {
		isOpen = $bindable(false),
		isFolder = $bindable(true),
		name = $bindable(''),
		onClose,
		onReset,
		onCreate
	}: Props = $props();
</script>

<Dialog.Root bind:open={isOpen}>
	<Dialog.Content
		onInteractOutside={onClose}
		class="fixed flex transform flex-col gap-4 overflow-auto p-6 transition-all duration-300 ease-in-out lg:max-w-md"
		showCloseButton={true}
		showResetButton={true}
		{onClose}
		{onReset}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[bi--hdd-stack-fill]"
					size="h-5 w-5"
					gap="gap-2"
					title="Create {isFolder ? 'Folder' : 'File'}"
				/>
			</Dialog.Title>
		</Dialog.Header>
		<div class="mt-2">
			<CustomValueInput
				placeholder={`Enter ${isFolder ? 'folder' : 'file'} name`}
				bind:value={name}
				classes="flex-1 space-y-1.5"
			/>
		</div>
		<Dialog.Footer class="mt-2">
			<div class="flex items-center justify-end space-x-4">
				<Button onclick={onCreate} size="sm" type="button" class="h-8 w-full lg:w-28">
					Create
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
