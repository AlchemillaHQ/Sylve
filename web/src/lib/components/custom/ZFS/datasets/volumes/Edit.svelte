<script lang="ts">
	import { editVolume } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { bytesToHumanReadable, isValidSize, parseQuotaToZFSBytes } from '$lib/utils/numbers';
	import { createVolProps } from '$lib/utils/zfs/dataset/volume';

	import Icon from '@iconify/svelte';
	import { toast } from 'svelte-sonner';

	type props = {
		checksum: string;
		compression: string;
		volblocksize: string;
		dedup: string;
		primarycache: string;
		volmode: string;
	};

	interface Props {
		open: boolean;
		dataset: Dataset;
	}

	let { open = $bindable(), dataset }: Props = $props();

	let options = {
		volsize: dataset.volsize ? bytesToHumanReadable(dataset.volsize) : '',
		volblocksize: dataset.volblocksize ? dataset.volblocksize.toString() : '16384',
		checksum: dataset.properties.checksum || 'on',
		compression: dataset.properties.compression || 'on',
		dedup: dataset.properties.dedup || 'off',
		primarycache: dataset.properties.primarycache || 'metadata',
		volmode: dataset.properties.volmode || 'dev'
	};

	let properties = $state(options);
	let zfsProperties = $state(createVolProps);

	async function edit() {
		if (!isValidSize(properties.volsize)) {
			toast.error('Invalid volume size', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await editVolume(dataset, {
			volsize: parseQuotaToZFSBytes(properties.volsize),
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			primarycache: properties.primarycache,
			volmode: properties.volmode
		});

		if (response.status === 'error') {
			if (response.error?.includes(`'volsize' must be a multiple of volume block size`)) {
				toast.error(
					`Size must be a multiple of volume block size (${dataset.volblocksize / 1024}K)`,
					{
						position: 'bottom-center'
					}
				);
			} else {
				toast.error('Failed to edit volume', {
					position: 'bottom-center'
				});
			}
		} else {
			toast.success('Volume edited successfully', {
				position: 'bottom-center'
			});

			open = false;
			properties = options;
		}
	}
</script>

{#snippet simpleSelect(
	prop: keyof props,
	label: string,
	placeholder: string,
	disabled: boolean = false
)}
	<SimpleSelect
		{label}
		{placeholder}
		options={zfsProperties[prop]}
		bind:value={properties[prop]}
		onChange={(value) => (properties[prop] = value)}
		{disabled}
	/>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-3xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<Icon icon="carbon:volume-block-storage" class="mr-2 h-5 w-5" />Edit Volume - {dataset.name}
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
						<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<div class="space-y-1">
					<Label class="w-24 whitespace-nowrap text-sm">Size</Label>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						bind:value={properties.volsize}
						placeholder="128M"
					/>
				</div>

				{@render simpleSelect('volblocksize', 'Block Size', 'Select block size', true)}
				{@render simpleSelect('checksum', 'Checksum', 'Select Checksum')}
				{@render simpleSelect('compression', 'Compression', 'Select compression type')}
				{@render simpleSelect('dedup', 'Deduplication', 'Select deduplication mode')}
				{@render simpleSelect('primarycache', 'Primary Cache', 'Select primary cache mode')}
				{@render simpleSelect('volmode', 'Volume Mode', 'Select volume mode')}
			</div>
		</div>

		<Dialog.Footer>
			<div class="mt-4 flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28"
					onclick={() => {
						edit();
					}}
				>
					Edit
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
