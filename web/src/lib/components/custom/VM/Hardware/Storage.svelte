<script lang="ts">
	import { getFiles } from '$lib/api/system/file-explorer';
	import { storageAttach, storageImport } from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import Icon from '@iconify/svelte';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { getPathParent, isValidAbsPath } from '$lib/utils/string';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
		vms: VM[];
	}

	let { open = $bindable(), datasets, downloads, vm, vms }: Props = $props();

	let options = {
		type: 'import' as 'import' | 'new',
		diskType: 'raw' as 'raw' | 'zvol' | 'image',
		rawPath: '',
		dataset: '',
		size: '',
		emulation: 'ahci-hd' as 'ahci-cd' | 'ahci-hd' | 'nvme' | 'virtio-blk'
	};

	let properties = $state(options);
	let usedDatasets = $derived.by(() => {
		const used = [] as string[];
		for (const m of vms) {
			for (const storage of m.storages) {
				if (storage.dataset && storage.dataset.guid) {
					used.push(storage.dataset.guid);
				}
			}
		}

		return used;
	});

	let zvolCombobox = $state({
		open: false,
		value: ''
	});

	async function attach() {
		const toastOptions = {
			position: 'bottom-center' as const
		};

		if (properties.type === 'import') {
			if (properties.diskType === 'raw') {
				if (!isValidAbsPath(properties.rawPath)) {
					toast.error('Invalid disk path', toastOptions);
					return;
				}

				const parent = getPathParent(properties.rawPath);
				const files = await getFiles(parent);
				const found = files.filter((file) => file.id === properties.rawPath);
				if (!found || found.length !== 1) {
					toast.error('Unable to find disk', toastOptions);
				}
			} else if (properties.diskType === 'zvol') {
				if (!zvolCombobox.value) {
					toast.error('Please select a ZFS Volume', toastOptions);
					return;
				}
			}

			const response = await storageImport(
				vm.vmId,
				properties.diskType as 'raw' | 'zvol',
				properties.diskType === 'raw' ? properties.rawPath : '',
				properties.diskType === 'zvol' ? zvolCombobox.value : '',
				properties.emulation
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to import disk', {
					position: 'bottom-center'
				});

				return;
			}

			toast.success('Disk imported', toastOptions);
			properties = options;
			open = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-md overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<Icon icon="grommet-icons:storage" class="h-5 w-5" />
					<span>New Storage</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4"
						onclick={() => {
							properties = options;
						}}
					>
						<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Reset'}</span>
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
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-2 gap-4">
			<SimpleSelect
				label="Type"
				placeholder="Select Type"
				options={[
					{ value: 'import', label: 'Import' },
					{ value: 'new', label: 'New' }
				]}
				bind:value={properties.type}
				onChange={(value) => (properties.type = value as 'import' | 'new')}
			/>

			<SimpleSelect
				label="Disk Type"
				placeholder="Select Disk Type"
				options={[
					{ value: 'zvol', label: 'ZFS Volume' },
					{ value: 'raw', label: 'Raw Disk' },
					...(properties.type !== 'import' ? [{ value: 'image', label: 'Image' }] : [])
				]}
				bind:value={properties.diskType}
				onChange={(value) => (properties.diskType = value as 'zvol' | 'raw')}
			/>
		</div>

		<div class="grid grid-cols-2 gap-4">
			{#if properties.type === 'import'}
				{#if properties.diskType === 'raw'}
					<CustomValueInput
						label="Raw Disk Path"
						placeholder="/tmp/openwrt-hdd.img"
						bind:value={properties.rawPath}
						classes="flex-1 space-y-1"
					/>
				{/if}

				{#if properties.diskType === 'zvol'}
					<CustomComboBox
						bind:open={zvolCombobox.open}
						label={'ZFS Volume'}
						bind:value={zvolCombobox.value}
						data={datasets
							.filter((dataset) => {
								return (
									dataset.type === 'volume' && !usedDatasets.some((used) => used === dataset.guid)
								);
							})
							.map((dataset) => ({
								value: dataset.guid || dataset.name,
								label: dataset.name
							}))}
						classes="flex-1 space-y-1"
						placeholder="Select ZFS Volume"
						width="w-3/4"
						multiple={false}
					></CustomComboBox>
				{/if}
			{/if}

			<SimpleSelect
				label="Emulation"
				placeholder="Select Emulation"
				options={[
					{ value: 'ahci-hd', label: 'AHCI Hard Disk' },
					{ value: 'ahci-cd', label: 'AHCI CD-ROM' },
					{ value: 'nvme', label: 'NVMe' },
					{ value: 'virtio-blk', label: 'VirtIO Block' }
				]}
				bind:value={properties.emulation}
				onChange={(value) =>
					(properties.emulation = value as 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk')}
			/>
		</div>

		<Dialog.Footer>
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28 "
					onclick={() => {
						attach();
					}}
				>
					Attach
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
