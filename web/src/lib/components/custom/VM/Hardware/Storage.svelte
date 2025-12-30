<script lang="ts">
	import { getFiles } from '$lib/api/system/file-explorer';
	import { storageImport, storageNew, storageUpdate } from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { getPathParent, isValidAbsPath } from '$lib/utils/string';
	import type { Zpool } from '$lib/types/zfs/pool';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { Debounced } from 'runed';
	import { roundUpToBlock } from '$lib/utils/zfs';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
		vms: VM[];
		pools: Zpool[];
		storageId: number | null;
		tableData: { rows: Row[]; columns: Column[] } | null;
		reload: boolean;
	}

	let {
		open = $bindable(),
		datasets,
		downloads,
		vm,
		vms,
		pools,
		storageId,
		tableData,
		reload = $bindable()
	}: Props = $props();
	let storages = $derived.by(() => vm.storages || []);

	let selectedStorage = $derived.by(() => {
		if (storageId === null) return null;
		return storages.find((s) => s.id === storageId) || null;
	});

	let selectedName = $derived.by(() => {
		if (storageId === null) return null;
		const storage = tableData?.rows.find((s) => s.id === storageId) || null;
		return storage ? storage.name : null;
	});

	let options = {
		name: '',
		type: 'new' as 'import' | 'new',
		diskType: 'zvol' as 'raw' | 'zvol' | 'image',
		rawPath: '',
		dataset: '',
		size: '',
		emulation: 'ahci-hd' as 'ahci-cd' | 'ahci-hd' | 'nvme' | 'virtio-blk',
		pool: '',
		bootOrder: null as number | null,
		loading: false
	};

	let editOptions = {
		name: selectedStorage ? selectedStorage.name || (selectedName ?? '') : '',
		size: selectedStorage ? humanFormat(selectedStorage.size, { unit: 'B' }) : '',
		emulation: selectedStorage
			? selectedStorage.emulation
			: ('ahci-hd' as 'ahci-cd' | 'ahci-hd' | 'nvme' | 'virtio-blk'),
		bootOrder: selectedStorage ? selectedStorage.bootOrder : 0,
		loading: false
	};

	let properties = $state(options);
	let editProperties = $state(editOptions);
	let lastRejectedSize: string | null = null;

	const debouncedSize = new Debounced(() => editProperties.size, 800);

	$effect(() => {
		if (!selectedStorage) return;

		const currentSize = selectedStorage.size || 0;
		const sizeStr = debouncedSize.current;

		if (!sizeStr) return;

		let newSize = 0;
		try {
			newSize = humanFormat.parse(sizeStr);
		} catch (e) {
			return;
		}

		const EPSILON = 1024 * 8;
		if (newSize < currentSize - EPSILON) {
			if (lastRejectedSize === sizeStr) return;
			lastRejectedSize = sizeStr;

			editProperties.size = humanFormat(currentSize, { unit: 'B' });
			toast.error('New size cannot be smaller than current size', {
				position: 'bottom-center'
			});
		} else {
			lastRejectedSize = null;
		}
	});

	let images = $derived(getISOs(downloads, true));
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

	let usedBootOrders = $derived.by(() => {
		const used = [] as number[];
		for (const storage of vm.storages) {
			if (storageId && storage.id === storageId) {
				continue;
			}

			if (storage.bootOrder || storage.bootOrder === 0) {
				used.push(storage.bootOrder);
			}
		}

		return used;
	});

	let zvolCombobox = $state({
		open: false,
		value: ''
	});

	let imageCombobox = $state({
		open: false,
		value: ''
	});

	const toastOptions = {
		position: 'bottom-center' as const
	};

	async function attach() {
		if (
			properties.name.trim() === '' ||
			properties.name.length === 0 ||
			properties.name.length > 128
		) {
			toast.error('Invalid storage name', toastOptions);
			return;
		}

		if (properties.pool === '' && properties.diskType !== 'image') {
			toast.error('No ZFS pool selected', toastOptions);
			return;
		}

		if (properties.bootOrder === null) {
			toast.error('Please specify a boot order', toastOptions);
			return;
		} else if (usedBootOrders.includes(Number(properties.bootOrder))) {
			toast.error('Boot order already in use', toastOptions);
			return;
		}

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

			properties.loading = true;

			const response = await storageImport(
				vm.rid,
				properties.name,
				imageCombobox.value,
				properties.diskType as 'raw' | 'zvol',
				properties.diskType === 'raw' ? properties.rawPath : '',
				properties.diskType === 'zvol' ? zvolCombobox.value : '',
				properties.emulation,
				properties.pool,
				Number(properties.bootOrder)
			);

			reload = true;

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
		} else if (properties.type === 'new') {
			if (properties.size === '') {
				toast.error('Please specify a size', toastOptions);
				return;
			}

			let parsedSize: number = 0;
			try {
				parsedSize = humanFormat.parse(properties.size);
			} catch (e) {
				toast.error('Invalid size format', toastOptions);
				return;
			}

			const response = await storageNew(
				vm.rid,
				properties.name,
				properties.diskType as 'zvol' | 'raw' | 'image',
				parsedSize,
				properties.emulation,
				properties.pool,
				Number(properties.bootOrder)
			);

			reload = true;

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to attach disk', {
					position: 'bottom-center'
				});

				return;
			}

			toast.success('Disk attached', toastOptions);
			properties = options;
			open = false;
		}
	}

	async function update() {
		if (
			editProperties.name.trim() === '' ||
			editProperties.name.length === 0 ||
			editProperties.name.length > 128
		) {
			toast.error('Invalid storage name', toastOptions);
			return;
		}

		if (editProperties.size === '') {
			toast.error('Please specify a size', toastOptions);
			return;
		}

		let parsedSize: number = 0;
		try {
			parsedSize = humanFormat.parse(editProperties.size);
		} catch (e) {
			toast.error('Invalid size format', toastOptions);
			return;
		}

		if (selectedStorage) {
			// Allow a small tolerance (epsilon) to avoid floating-point rounding issues
			const EPSILON = 1024; // 1 KB tolerance
			if (parsedSize < selectedStorage.size - EPSILON) {
				toast.error('New size cannot be smaller than current size', toastOptions);
				return;
			}
		}

		if (editProperties.bootOrder === null) {
			toast.error('Please specify a boot order', toastOptions);
			return;
		}

		if (usedBootOrders.includes(Number(editProperties.bootOrder))) {
			toast.error('Boot order already in use', toastOptions);
			return;
		}

		const dataset = selectedStorage?.dataset;
		const blockSize =
			datasets.find((d) => d.guid === dataset?.guid)?.properties?.volblocksize || 8192;
		const roundedSize = roundUpToBlock(parsedSize, Number(blockSize));

		editProperties.loading = true;
		const response = await storageUpdate(
			selectedStorage ? selectedStorage.id : 0,
			editProperties.name,
			roundedSize,
			editProperties.emulation,
			Number(editProperties.bootOrder)
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to update storage', {
				position: 'bottom-center'
			});

			return;
		}

		toast.success('Storage updated', toastOptions);
		editProperties = editOptions;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class={selectedStorage
			? 'w-1/3 overflow-hidden p-5 lg:max-w-2xl'
			: 'w-full overflow-hidden p-5 lg:max-w-2xl'}
	>
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[grommet-icons--storage] h-5 w-5"></span>
					{selectedName ? `Edit - ${selectedName}` : 'New Storage'}
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4"
						onclick={() => {
							if (selectedStorage) {
								editProperties = editOptions;
							} else {
								properties = options;
							}
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							if (selectedStorage) {
								editProperties = editOptions;
							} else {
								properties = options;
							}
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if !selectedStorage}
			<CustomValueInput
				label="Name"
				placeholder="DB Storage"
				bind:value={properties.name}
				classes="flex-1 space-y-1"
			/>

			<div class="grid grid-cols-3 gap-4">
				<SimpleSelect
					label="Type"
					placeholder="Select Type"
					options={[
						{ value: 'new', label: 'New' },
						{ value: 'import', label: 'Import' }
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
						...(properties.type !== 'new' ? [{ value: 'image', label: 'Image' }] : [])
					]}
					bind:value={properties.diskType}
					onChange={(value) => (properties.diskType = value as 'zvol' | 'raw')}
				/>

				<SimpleSelect
					label="Pool"
					placeholder="Select Pool"
					options={pools.map((pool) => ({ value: pool.name, label: pool.name }))}
					bind:value={properties.pool}
					onChange={(value) => (properties.pool = value as string)}
					disabled={properties.diskType === 'image'}
				/>
			</div>

			<div class="grid grid-cols-3 gap-4">
				{#if properties.type === 'import'}
					{#if properties.diskType === 'image'}
						<CustomComboBox
							bind:open={imageCombobox.open}
							label={'ISO Image'}
							bind:value={imageCombobox.value}
							data={images}
							classes="flex-1 space-y-1"
							placeholder="Select ISO Image"
							width="w-3/4"
							multiple={false}
						></CustomComboBox>
					{:else if properties.diskType === 'raw'}
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
										dataset.type === GZFSDatasetTypeSchema.enum.VOLUME &&
										!usedDatasets.some((used) => used === dataset.guid)
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
				{:else if properties.type === 'new' && properties.diskType !== 'image'}
					<CustomValueInput
						label="Size"
						placeholder={humanFormat(10 * 1024 * 1024 * 1024, { unit: 'B' })}
						bind:value={properties.size}
						classes="flex-1 space-y-1"
					/>
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

				<CustomValueInput
					label="Boot Order"
					placeholder="2"
					type="number"
					bind:value={properties.bootOrder as number}
					classes="flex-1 space-y-1"
				/>
			</div>
		{:else}
			<CustomValueInput
				label="Name"
				placeholder="DB Storage"
				bind:value={editProperties.name}
				classes="flex-1 space-y-1"
			/>

			<div class="grid grid-cols-3 gap-4">
				<CustomValueInput
					label="Size"
					placeholder={humanFormat(10 * 1024 * 1024 * 1024, { unit: 'B' })}
					bind:value={editProperties.size}
					classes="flex-1 space-y-1"
				/>

				<SimpleSelect
					label="Emulation"
					placeholder="Select Emulation"
					options={[
						{ value: 'ahci-hd', label: 'AHCI Hard Disk' },
						{ value: 'ahci-cd', label: 'AHCI CD-ROM' },
						{ value: 'nvme', label: 'NVMe' },
						{ value: 'virtio-blk', label: 'VirtIO Block' }
					]}
					bind:value={editProperties.emulation}
					onChange={(value) =>
						(editProperties.emulation = value as 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk')}
				/>

				<CustomValueInput
					label="Boot Order"
					placeholder="2"
					type="number"
					bind:value={editProperties.bootOrder as number}
					classes="flex-1 space-y-1"
				/>
			</div>
		{/if}

		<Dialog.Footer>
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28 "
					onclick={() => {
						if (selectedStorage) {
							update();
						} else {
							attach();
						}
					}}
					disabled={properties.loading || editProperties.loading}
				>
					{#if properties.loading || editProperties.loading}
						<span class="icon-[eos-icons--loading] mr-2 h-4 w-4 animate-spin"></span>
					{:else}
						<span>{selectedStorage ? 'Save Changes' : 'Attach Storage'}</span>
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
