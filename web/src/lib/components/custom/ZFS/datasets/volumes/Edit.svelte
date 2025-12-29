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
	import humanFormat from 'human-format';
	import { watch } from 'runed';
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
		reload?: boolean;
	}

	function parseBytes(input: string): number {
		if (!input || input.trim() === '') return 0;

		const parsed = humanFormat.parse(input, { unit: 'B' });
		return Number.isFinite(parsed) ? parsed : 0;
	}

	let { open = $bindable(), dataset, reload = $bindable() }: Props = $props();

	let options = {
		volsize: dataset.properties?.volsize
			? bytesToHumanReadable(Number(dataset.properties.volsize), true)
			: '',
		volblocksize: dataset.properties?.volblocksize
			? dataset.properties.volblocksize.toString()
			: '16384',
		checksum: dataset.properties?.checksum || 'on',
		compression: dataset.properties?.compression || 'lz4',
		dedup: dataset.properties?.dedup || 'off',
		primarycache: dataset.properties?.primarycache || 'metadata',
		volmode: dataset.properties?.volmode || 'dev'
	};

	let properties = $state(options);
	let zfsProperties = $state(createVolProps);
	let invalidSizeForBlockSize = $state(false);

	async function edit() {
		if (!isValidSize(properties.volsize)) {
			toast.error('Invalid volume size', { position: 'bottom-center' });
			return;
		}

		let volsizeBytes = parseQuotaToZFSBytes(properties.volsize);

		const response = await editVolume(dataset.guid, {
			volsize: volsizeBytes,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			primarycache: properties.primarycache,
			volmode: properties.volmode
		});

		reload = true;

		if (response.status === 'error') {
			if (response.error?.includes(`'volsize' must be a multiple of volume block size`)) {
				toast.error('Volume size must match block size', {
					position: 'bottom-center'
				});
				return;
			}

			toast.error('Failed to edit volume', { position: 'bottom-center' });
			return;
		}

		toast.success(`Volume ${dataset.name} edited`, {
			position: 'bottom-center'
		});

		open = false;
		properties = options;
	}

	watch(
		() => properties.volsize,
		() => {
			const blockSize = Number(properties.volblocksize); // bytes
			const sizeBytes = parseBytes(properties.volsize);

			invalidSizeForBlockSize = sizeBytes % blockSize !== 0;
		}
	);
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
		class="fixed top-1/2 left-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-3xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[carbon--volume-block-storage] mr-2 h-5 w-5"></span>

					Edit Volume - {dataset.name}
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
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<div class="space-y-1">
					<Label class="w-24 text-sm whitespace-nowrap">
						<div class="flex items-center gap-1">
							<span>Volume Size</span>
							{#if invalidSizeForBlockSize}
								<span
									class="icon-[mdi--alert-circle-outline] h-4 w-4 text-yellow-500"
									title="Volume size is not a multiple of block size, click on this if you'd like to automatically adjust it"
									onclick={() => {
										const blockSize = Number(properties.volblocksize);
										const sizeBytes = parseBytes(properties.volsize);
										const oneMB = 1024 * 1024 * 2;
										const adjustedToNextMB = Math.ceil((sizeBytes + 1) / oneMB) * oneMB;
										const finalBytes = Math.ceil(adjustedToNextMB / blockSize) * blockSize;

										properties.volsize = humanFormat(finalBytes, { unit: 'B', decimals: 10 });
									}}
								></span>
							{/if}
						</div>
					</Label>
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
