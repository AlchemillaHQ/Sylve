<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';

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
		class="fixed flex transform flex-col gap-4 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-md"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[bi--hdd-stack-fill] h-5 w-5"></span>
					Create {isFolder ? 'Folder' : 'File'}
				</div>
				<div class="flex items-center gap-0.5">
					<Button onclick={onReset} size="sm" variant="link" class="h-4" title="Reset">
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>

					<Button size="sm" variant="link" class="h-4" title="Close" onclick={onClose}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
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
