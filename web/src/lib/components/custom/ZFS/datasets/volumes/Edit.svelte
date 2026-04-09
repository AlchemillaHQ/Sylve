<script lang="ts">
	import { editVolume } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import {
		normalizeSizeInputExact,
		parseSizeInputToBytes,
		toZfsBytesString
	} from '$lib/utils/bytes';
	import { createVolProps } from '$lib/utils/zfs/dataset/volume';
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

	let { open = $bindable(), dataset, reload = $bindable() }: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		volsize: dataset.properties?.volsize
			? (normalizeSizeInputExact(dataset.properties.volsize) ?? '')
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
	let volblocksizeOpen = $state(false);
	let compressionOpen = $state(false);

	const volblocksizeData = $derived.by(() => {
		const base = zfsProperties.volblocksize;
		const val = properties.volblocksize;
		if (!val || base.some((d) => d.value === val)) return base;
		const humanized = normalizeSizeInputExact(val);
		const label = humanized ? `${humanized} - Custom` : `${val} - Custom`;
		return [{ value: val, label }, ...base];
	});

	async function edit() {
		const parsedVolsize = parseSizeInputToBytes(properties.volsize);
		if (parsedVolsize === null) {
			toast.error('Invalid volume size', { position: 'bottom-center' });
			return;
		}

		const volsizeBytes = toZfsBytesString(parsedVolsize);

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
			const blockSize = Number(properties.volblocksize);
			const sizeBytes = parseSizeInputToBytes(properties.volsize) ?? 0;

			invalidSizeForBlockSize = sizeBytes % blockSize !== 0;
		}
	);

	function adjustBlockSize() {
		const blockSize = Number(properties.volblocksize);
		const sizeBytes = parseSizeInputToBytes(properties.volsize) ?? 0;
		const oneMB = 1024 * 1024 * 2;
		const adjustedToNextMB = Math.ceil((sizeBytes + 1) / oneMB) * oneMB;
		const finalBytes = Math.ceil(adjustedToNextMB / blockSize) * blockSize;

		properties.volsize = normalizeSizeInputExact(finalBytes) ?? `${finalBytes} B`;
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
		classes={{
			parent: 'flex-1 min-w-0 space-y-1',
			label: 'flex h-7 items-center whitespace-nowrap text-sm',
			trigger:
				'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
		}}
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
						title="Reset"
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
						title="Close"
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
					<div class="flex h-7 items-center gap-1">
						<Label class="whitespace-nowrap text-sm">Volume Size</Label>
						{#if invalidSizeForBlockSize}
							<span
								role="button"
								tabindex="0"
								class="icon-[mdi--alert-circle-outline] h-4 w-4 text-yellow-500"
								title="Volume size is not a multiple of block size, click on this if you'd like to automatically adjust it"
								onclick={() => {
									adjustBlockSize();
								}}
								onkeydown={(e) => {
									if (e.key === 'Enter' || e.key === ' ') {
										e.preventDefault();
										adjustBlockSize();
									}
								}}
							></span>
						{/if}
					</div>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						bind:value={properties.volsize}
						placeholder="128M"
						onblur={() => {
							const normalized = normalizeSizeInputExact(properties.volsize);
							if (normalized !== null) {
								properties.volsize = normalized;
							}
						}}
					/>
				</div>

				<CustomComboBox
					bind:open={volblocksizeOpen}
					label="Block Size"
					bind:value={properties.volblocksize}
					data={volblocksizeData}
					classes="space-y-1"
					placeholder="Select block size"
					triggerWidth="w-full"
					width="w-full"
					disabled={true}
				/>
				{@render simpleSelect('checksum', 'Checksum', 'Select Checksum')}
				<CustomComboBox
					bind:open={compressionOpen}
					label="Compression"
					bind:value={properties.compression}
					data={zfsProperties.compression}
					classes="space-y-1"
					placeholder="Select or type compression"
					triggerWidth="w-full"
					width="w-full"
					allowCustom={true}
				/>
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
