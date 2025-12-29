<script lang="ts">
	import { createSnapshot } from '$lib/api/zfs/datasets';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidDatasetName } from '$lib/utils/zfs';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		dataset: Dataset;
		recursion?: boolean;
		reload?: boolean;
	}

	let { open = $bindable(), dataset, recursion = false, reload = $bindable() }: Props = $props();
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
			const response = await createSnapshot(dataset, properties.name, properties.recursive);
			reload = true;

			if (response.status === 'success') {
				toast.success(`Snapshot ${dataset.name}@${properties.name} created`, {
					position: 'bottom-center'
				});
			} else {
				if (response.error) {
					if (response.error.endsWith('dataset already exists')) {
						toast.error(`Snapshot ${dataset.name}@${properties.name} already exists`, {
							position: 'bottom-center'
						});
					} else {
						handleAPIError(response);
						toast.error(`Failed to create snapshot`, {
							position: 'bottom-center'
						});
					}
				}
			}
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
		class="flex flex-col p-5"
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] h-6 w-6"></span>

					<span>
						Snapshot - {properties.name !== ''
							? `${dataset.name}@${properties.name}`
							: `${dataset.name}`}
					</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
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
						variant="link"
						class="h-4"
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
			</Dialog.Title>
		</Dialog.Header>

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
				class="w-full lg:w-28"
				onclick={() => {
					create();
				}}>Create</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
