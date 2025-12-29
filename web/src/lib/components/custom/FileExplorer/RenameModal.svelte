<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';

	interface Props {
		isOpen: boolean;
		newName: string;
		onClose: () => void;
		onReset: () => void;
		onRename: () => void;
	}

	let {
		isOpen = $bindable(false),
		newName = $bindable(''),
		onClose,
		onReset,
		onRename
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
					<span class="icon-[mdi--rename-box-outline] h-6 w-6"></span>
					Rename
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
				placeholder="Enter new name"
				bind:value={newName}
				classes="flex-1 space-y-1.5"
			/>
		</div>
		<Dialog.Footer class="mt-2">
			<div class="flex items-center justify-end space-x-4">
				<Button size="sm" type="button" class="h-8 w-full lg:w-28" onclick={onRename}>
					Rename
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
