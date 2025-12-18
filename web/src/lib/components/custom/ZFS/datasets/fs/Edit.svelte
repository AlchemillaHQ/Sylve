<script lang="ts">
	import { editFileSystem } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { bytesToHumanReadable, isValidSize, parseQuotaToZFSBytes } from '$lib/utils/numbers';
	import { createFSProps } from '$lib/utils/zfs/dataset/fs';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		dataset: Dataset;
		reload?: boolean;
	}

	let { open = $bindable(), dataset, reload = $bindable() }: Props = $props();
	let options = {
		atime: dataset.properties?.atime || 'on',
		checksum: dataset.properties?.checksum || 'on',
		compression: dataset.properties?.compression || 'lz4',
		dedup: dataset.properties?.dedup || 'off',
		quota: dataset.properties?.quota
			? bytesToHumanReadable(parseInt(dataset.properties.quota))
			: '',
		aclinherit: dataset.properties?.aclinherit || 'passthrough',
		aclmode: dataset.properties?.aclmode || 'passthrough',
		recordsize: dataset.properties?.recordsize || '128K',
		mountpoint: dataset.properties?.mountpoint || ''
	};

	let zfsProperties = $state(createFSProps);
	let properties = $state(options);

	async function edit() {
		if (properties.quota !== '') {
			if (!isValidSize(properties.quota)) {
				toast.error('Invalid quota size', {
					position: 'bottom-center'
				});
				return;
			}
		}

		const response = await editFileSystem(dataset.guid as string, {
			atime: properties.atime,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			quota: parseQuotaToZFSBytes(properties.quota),
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
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--files] h-5 w-5"></span>
					Edit Filesystem - {dataset.name}
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
				<SimpleSelect
					label="ATime"
					placeholder="Select ATime"
					options={zfsProperties.atime}
					bind:value={properties.atime}
					onChange={(value) => (properties.atime = value)}
				/>

				<SimpleSelect
					label="Checksum"
					placeholder="Select Checksum"
					options={zfsProperties.checksum}
					bind:value={properties.checksum}
					onChange={(value) => (properties.checksum = value)}
				/>

				<SimpleSelect
					label="Compression"
					placeholder="Select Compression"
					options={zfsProperties.compression}
					bind:value={properties.compression}
					onChange={(value) => (properties.compression = value)}
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
				/>

				<SimpleSelect
					label="Record Size"
					placeholder="Select Record Size"
					options={zfsProperties.recordsize}
					bind:value={properties.recordsize}
					onChange={(value) => (properties.recordsize = value)}
				/>

				<div class="space-y-1.5">
					<Label for="mountpoint" class="w-24 whitespace-nowrap text-sm">Custom Mount Point</Label>
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
