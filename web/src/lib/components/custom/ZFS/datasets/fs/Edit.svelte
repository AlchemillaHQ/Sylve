<script lang="ts">
	import { editFileSystem } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
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
	import { handleAPIError } from '$lib/utils/http';
	import { createFSProps } from '$lib/utils/zfs/dataset/fs';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		dataset: Dataset;
		reload?: boolean;
	}

	let { open = $bindable(), dataset, reload = $bindable() }: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		atime: dataset.properties?.atime || 'on',
		checksum: dataset.properties?.checksum || 'on',
		compression: dataset.properties?.compression || 'lz4',
		dedup: dataset.properties?.dedup || 'off',
		quota: dataset.properties?.quota
			? normalizeSizeInputExact(parseInt(dataset.properties.quota)) === '0 B'
				? ''
				: normalizeSizeInputExact(parseInt(dataset.properties.quota))
			: '',
		aclinherit: dataset.properties?.aclinherit || 'passthrough',
		aclmode: dataset.properties?.aclmode || 'passthrough',
		recordsize: dataset.properties?.recordsize || '128K',
		mountpoint: dataset.properties?.mountpoint || ''
	};

	let zfsProperties = $state(createFSProps);
	let properties = $state(options);
	let compressionOpen = $state(false);
	let recordsizeOpen = $state(false);

	const recordsizeData = $derived.by(() => {
		const base = zfsProperties.recordsize;
		const val = properties.recordsize;
		if (!val || base.some((d) => d.value === val)) return base;
		const humanized = normalizeSizeInputExact(val);
		const label = humanized ? `${humanized} - Custom` : `${val} - Custom`;
		return [{ value: val, label }, ...base];
	});

	async function edit() {
		let quota = '0B';
		if (properties.quota !== '') {
			const parsed = parseSizeInputToBytes(properties.quota);
			if (parsed === null) {
				toast.error('Invalid quota size', {
					position: 'bottom-center'
				});
				return;
			}

			quota = toZfsBytesString(parsed);
		}

		const response = await editFileSystem(dataset.guid as string, {
			atime: properties.atime,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			quota: quota,
			aclinherit: properties.aclinherit,
			aclmode: properties.aclmode,
			recordsize: properties.recordsize,
			mountpoint: properties.mountpoint || ''
		});

		reload = true;

		if (response.status === 'error') {
			handleAPIError(response);

			if (response.error?.includes('size is less than current used or reserved space')) {
				toast.error('Quota size is less than current used or reserved space', {
					position: 'bottom-center'
				});
				return;
			}

			toast.error('Failed to edit filesystem', {
				position: 'bottom-center'
			});

			return;
		}

		let n = dataset.name;
		toast.success(`File System ${n} edited`, {
			position: 'bottom-center'
		});

		properties = options;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-2xl"
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
					icon="icon-[material-symbols--files]"
					size="h-5 w-5"
					gap="gap-2"
					title="Edit Filesystem - {dataset.name}"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<SimpleSelect
					label="ATime"
					placeholder="Select ATime"
					options={zfsProperties.atime}
					bind:value={properties.atime}
					onChange={(value) => (properties.atime = value)}
					classes={{
						parent: 'flex-1 min-w-0 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<SimpleSelect
					label="Checksum"
					placeholder="Select Checksum"
					options={zfsProperties.checksum}
					bind:value={properties.checksum}
					onChange={(value) => (properties.checksum = value)}
					classes={{
						parent: 'flex-1 min-w-0 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

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

				<SimpleSelect
					label="Deduplication"
					placeholder="Select Deduplication"
					options={zfsProperties.dedup}
					bind:value={properties.dedup}
					onChange={(value) => (properties.dedup = value)}
				/>

				<div class="space-y-1">
					<Label class="w-24 whitespace-nowrap text-sm">Quota</Label>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						bind:value={properties.quota}
						placeholder="256M (Empty for no quota)"
						onblur={() => {
							if (properties.quota === '') return;
							const normalized = normalizeSizeInputExact(properties.quota);
							if (normalized !== null) {
								properties.quota = normalized;
							}
						}}
					/>
				</div>

				<SimpleSelect
					label="ACL Inherit"
					placeholder="Select ACL Inherit"
					options={zfsProperties.aclInherit}
					bind:value={properties.aclinherit}
					onChange={(value) => (properties.aclinherit = value)}
				/>

				<SimpleSelect
					label="ACL Mode"
					placeholder="Select ACL Mode"
					options={zfsProperties.aclMode}
					bind:value={properties.aclmode}
					onChange={(value) => (properties.aclmode = value)}
					classes={{
						parent: 'flex-1 min-w-0 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<CustomComboBox
					bind:open={recordsizeOpen}
					label="Record Size"
					bind:value={properties.recordsize}
					data={recordsizeData}
					classes="space-y-1"
					placeholder="Select or type record size"
					triggerWidth="w-full"
					width="w-full"
					allowCustom={true}
				/>

				<div class="space-y-1">
					<Label for="mountpoint" class="flex h-7 items-center whitespace-nowrap text-sm"
						>Custom Mount Point</Label
					>
					<Input
						type="text"
						id="mountpoint"
						placeholder="/custom/mountpoint"
						autocomplete="off"
						bind:value={properties.mountpoint}
					/>
				</div>
			</div>
		</div>

		<Dialog.Footer>
			<div class="mt-4 flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-28"
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
