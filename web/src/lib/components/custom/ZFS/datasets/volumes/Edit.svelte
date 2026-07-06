<script lang="ts">
	import { editVolume } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
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
			readonly: string;
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
			readonly: dataset.properties?.readonly || 'off',
			refreservation:
			dataset.properties?.refreservation && dataset.properties.refreservation !== 'none'
				? (normalizeSizeInputExact(dataset.properties.refreservation) ?? '')
				: '',
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

			const props: Record<string, string> = {
				volsize: volsizeBytes,
				checksum: properties.checksum,
				compression: properties.compression,
				dedup: properties.dedup,
				readonly: properties.readonly,
				primarycache: properties.primarycache,
				volmode: properties.volmode
			};

		if (properties.refreservation) {
			const parsed = parseSizeInputToBytes(properties.refreservation);
			if (parsed !== null && parsed > 0) {
				props.refreservation = toZfsBytesString(parsed);
			}
		}

		const response = await editVolume(dataset.guid, props);

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
		showCloseButton={true}
		showResetButton={true}
		onReset={() => {
			properties = options;
		}}
		onClose={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[carbon--volume-block-storage]"
					size="h-5 w-5"
					gap="gap-2"
					title="Edit Volume - {dataset.name}"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<CustomValueInput
					label="Volume Size"
					placeholder="128M"
					bind:value={properties.volsize}
					classes="flex-1 space-y-1"
					topRightButton={invalidSizeForBlockSize
						? {
								icon: 'icon-[mdi--alert-circle-outline]',
								tooltip: 'Volume size is not a multiple of block size, click to adjust',
								function: async () => {
									adjustBlockSize();
									return properties.volsize;
								}
							}
						: undefined}
					onBlur={() => {
						const normalized = normalizeSizeInputExact(properties.volsize);
						if (normalized !== null) {
							properties.volsize = normalized;
						}
					}}
				/>

				<CustomValueInput
					label="Referenced Reservation"
					placeholder="10G (optional)"
					bind:value={properties.refreservation}
					classes="flex-1 space-y-1"
					hint="Minimum guaranteed space for referenced data. Leave empty to keep current value."
					onBlur={() => {
						const normalized = normalizeSizeInputExact(properties.refreservation);
						if (normalized !== null) {
							properties.refreservation = normalized;
						}
					}}
				/>

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
				{@render simpleSelect('readonly', 'Read Only', 'Select read only mode')}
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
