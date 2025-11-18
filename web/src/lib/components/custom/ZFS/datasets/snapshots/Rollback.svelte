<script lang="ts">
	import { createSnapshot } from '$lib/api/zfs/datasets';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { isValidDatasetName } from '$lib/utils/zfs';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		dataset: Dataset;
		recursion?: boolean;
	}

	let { open = $bindable(), dataset, recursion = false }: Props = $props();
	let options = {
		name: '',
		recursive: false
	};

	let properties = $state(options);

	async function create() {
		if (properties.name === '' || !isValidDatasetName(properties.name)) {
			toast.error('Invalid name', {
				position: 'bottom-center'
			});

			return;
		}

		try {
			await createSnapshot(dataset, properties.name, properties.recursive);
			toast.success(`Snapshot ${dataset.name}@${properties.name} created`, {
				position: 'bottom-center'
			});
			open = false;
			properties = options;
		} catch (error) {
			toast.error(`Failed to create snapshot`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex flex-col"
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
	>
		<div class="flex w-full items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] h-6 w-6"></span>

						<span>
							Snapshot - {properties.name !== ''
								? `${dataset.name}@${properties.name}`
								: `${dataset.name}`}
						</span>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="ghost"
					class="h-8"
					title={'Reset'}
					onclick={() => {
						properties = options;
					}}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>

					<span class="sr-only">Reset</span>
				</Button>
				<Button
					size="sm"
					variant="ghost"
					class="h-8"
					title={'Close'}
					onclick={() => {
						open = false;
						properties = options;
					}}
				>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>

					<span class="sr-only">Close</span>
				</Button>
			</div>
		</div>

		<CustomValueInput
			label={`${'Name'} | ${'Prefix'}`}
			placeholder="after-upgrade"
			bind:value={properties.name}
			classes="flex-1 space-y-1"
		/>

		{#if recursion}
			<CustomCheckbox
				label="Recursive"
				bind:checked={properties.recursive}
				classes="flex items-center gap-2"
			></CustomCheckbox>
		{/if}

		<Dialog.Footer>
			<Button
				size="sm"
				onclick={() => {
					create();
				}}>Create</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
