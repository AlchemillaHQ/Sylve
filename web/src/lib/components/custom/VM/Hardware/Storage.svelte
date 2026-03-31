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
	import { normalizeSizeInputExact, parseSizeInputToBytes } from '$lib/utils/bytes';
	import { handleAPIError } from '$lib/utils/http';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import { toast } from 'svelte-sonner';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { getPathParent, isValid9PTargetName, isValidAbsPath } from '$lib/utils/string';
	import type { Zpool } from '$lib/types/zfs/pool';
	import type { Column, Row } from '$lib/types/components/tree-table';
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
	let selectedStorageDisplaySize = $derived.by(() => {
		if (storageId === null) {
			return selectedStorage?.size || 0;
		}

		const tableStorage = tableData?.rows.find((s) => s.id === storageId) || null;
		const tableSize = Number(tableStorage?.size);
		if (Number.isFinite(tableSize) && tableSize > 0) {
			return tableSize;
		}

		return selectedStorage?.size || 0;
	});
	let selectedStorageType = $derived.by(() => selectedStorage?.type ?? null);
	let isImageStorageEdit = $derived.by(() => selectedStorageType === 'image');
	let isFilesystemStorageEdit = $derived.by(() => selectedStorageType === 'filesystem');

	type StorageAttachType = 'import' | 'new';
	type StorageDiskType = 'raw' | 'zvol' | 'image' | 'filesystem';
	type StorageEmulation = 'ahci-cd' | 'ahci-hd' | 'nvme' | 'virtio-blk' | 'virtio-9p';

	let options = {
		name: '',
		type: 'new' as StorageAttachType,
		diskType: 'zvol' as StorageDiskType,
		rawPath: '',
		dataset: '',
		size: '',
		filesystemTarget: '',
		filesystemReadOnly: false,
		emulation: 'ahci-hd' as StorageEmulation,
		pool: '',
		bootOrder: null as number | null,
		loading: false
	};

	// svelte-ignore state_referenced_locally
	let editOptions = {
		name: selectedStorage ? selectedStorage.name || (selectedName ?? '') : '',
		size: selectedStorage ? (normalizeSizeInputExact(selectedStorageDisplaySize) ?? '') : '',
		emulation: selectedStorage
			? selectedStorage.emulation
			: ('ahci-hd' as StorageEmulation),
		filesystemTarget: selectedStorage?.filesystemTarget || '',
		filesystemReadOnly: selectedStorage?.readOnly ?? false,
		enable: selectedStorage ? (selectedStorage.enable ?? true) : true,
		bootOrder: selectedStorage ? selectedStorage.bootOrder : 0,
		loading: false
	};

	let properties = $state(options);
	let editProperties = $state(editOptions);

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

	let filesystemCombobox = $state({
		open: false,
		value: ''
	});

	let filesystemDatasetOptions = $derived.by(() =>
		datasets
			.filter((dataset) => dataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM)
			.map((dataset) => ({
				value: dataset.guid || dataset.name,
				label: `${dataset.name} (${dataset.mountpoint})`
			}))
	);

	$effect(() => {
		if (properties.diskType === 'filesystem' && properties.type !== 'new') {
			properties.type = 'new';
		}

		if (properties.diskType === 'filesystem') {
			properties.emulation = 'virtio-9p';
		} else if (properties.emulation === 'virtio-9p') {
			properties.emulation = 'ahci-hd';
		}
	});

	$effect(() => {
		if (isFilesystemStorageEdit) {
			editProperties.emulation = 'virtio-9p';
		}
	});

	const toastOptions = {
		position: 'bottom-center' as const
	};

	function handleEditSizeBlur() {
		if (isImageStorageEdit || isFilesystemStorageEdit) {
			return;
		}

		if (editProperties.size.trim() === '') {
			return;
		}

		const parsed = parseSizeInputToBytes(editProperties.size);
		if (parsed === null) {
			return;
		}

		if (selectedStorage) {
			const EPSILON = 1024; // 1 KB tolerance
			if (parsed < selectedStorage.size - EPSILON) {
				editProperties.size = normalizeSizeInputExact(selectedStorage.size) ?? '0 B';
				toast.error('New size cannot be smaller than current size', toastOptions);
				return;
			}
		}

		const normalized = normalizeSizeInputExact(parsed);
		if (normalized !== null) {
			editProperties.size = normalized;
		}
	}

	async function attach() {
		if (
			properties.name.trim() === '' ||
			properties.name.length === 0 ||
			properties.name.length > 128
		) {
			toast.error('Invalid storage name', toastOptions);
			return;
		}

		if (
			properties.pool === '' &&
			properties.diskType !== 'image' &&
			properties.diskType !== 'filesystem'
		) {
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
				(properties.emulation === 'virtio-9p' ? 'ahci-hd' : properties.emulation),
				properties.pool,
				Number(properties.bootOrder)
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to import storage', {
					position: 'bottom-center'
				});

				return;
			}

			toast.success('Storage imported', toastOptions);
			reload = true;
			properties = options;
			open = false;
		} else if (properties.type === 'new') {
			let parsedSize: number | undefined = undefined;
			if (properties.diskType !== 'image' && properties.diskType !== 'filesystem') {
				if (properties.size === '') {
					toast.error('Please specify a size', toastOptions);
					return;
				}

				const parsed = parseSizeInputToBytes(properties.size);
				if (parsed === null) {
					toast.error('Invalid size format', toastOptions);
					return;
				}
				parsedSize = parsed;
			}

			if (properties.diskType === 'filesystem') {
				if (!filesystemCombobox.value) {
					toast.error('Please select a ZFS filesystem dataset', toastOptions);
					return;
				}

				if (!isValid9PTargetName(properties.filesystemTarget.trim())) {
					toast.error("Invalid 9P target name (letters, numbers, '.', '_' and '-' only)", toastOptions);
					return;
				}
			}

			const response = await storageNew(
				vm.rid,
				properties.name,
				properties.diskType,
				parsedSize,
				properties.diskType === 'filesystem' ? 'virtio-9p' : properties.emulation,
				properties.pool,
				Number(properties.bootOrder),
				properties.diskType === 'filesystem' ? filesystemCombobox.value : '',
				properties.diskType === 'filesystem' ? properties.filesystemTarget.trim() : '',
				properties.diskType === 'filesystem' ? properties.filesystemReadOnly : false
			);

			reload = true;

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to attach storage', {
					position: 'bottom-center'
				});

				return;
			}

			toast.success('Storage attached', toastOptions);
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

		let parsedSize: number | undefined = undefined;
		if (!isImageStorageEdit && !isFilesystemStorageEdit) {
			if (editProperties.size === '') {
				toast.error('Please specify a size', toastOptions);
				return;
			}

			const parsed = parseSizeInputToBytes(editProperties.size);
			if (parsed === null) {
				toast.error('Invalid size format', toastOptions);
				return;
			}
			parsedSize = parsed;

			if (selectedStorage) {
				// Allow a small tolerance (epsilon) to avoid floating-point rounding issues
				const EPSILON = 1024; // 1 KB tolerance
				if (parsedSize < selectedStorage.size - EPSILON) {
					toast.error('New size cannot be smaller than current size', toastOptions);
					return;
				}
			}
		}

		if (
			isFilesystemStorageEdit &&
			!isValid9PTargetName((editProperties.filesystemTarget || '').trim())
		) {
			toast.error("Invalid 9P target name (letters, numbers, '.', '_' and '-' only)", toastOptions);
			return;
		}

		if (editProperties.bootOrder === null) {
			toast.error('Please specify a boot order', toastOptions);
			return;
		}

		if (usedBootOrders.includes(Number(editProperties.bootOrder))) {
			toast.error('Boot order already in use', toastOptions);
			return;
		}

		let roundedSize: number | undefined = undefined;
		if (parsedSize !== undefined) {
			const dataset = selectedStorage?.dataset;
			const fDataset = datasets.find((d) => d.guid === dataset?.guid);

			let blockSize = 8192;
			if (fDataset && fDataset.properties) {
				if (
					fDataset.type === GZFSDatasetTypeSchema.enum.VOLUME &&
					fDataset.properties.volblocksize
				) {
					blockSize = Number(fDataset.properties.volblocksize);
				} else if (
					fDataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM &&
					fDataset.properties.recordsize
				) {
					blockSize = Number(fDataset.properties.recordsize);
				}
			}

			roundedSize = roundUpToBlock(parsedSize, blockSize);
		}

		editProperties.loading = true;
		const response = await storageUpdate(
			selectedStorage ? selectedStorage.id : 0,
			editProperties.name,
			roundedSize,
			isFilesystemStorageEdit ? 'virtio-9p' : editProperties.emulation,
			Number(editProperties.bootOrder),
			editProperties.enable,
			isFilesystemStorageEdit ? editProperties.filesystemTarget.trim() : undefined,
			isFilesystemStorageEdit ? editProperties.filesystemReadOnly : undefined
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
			? 'w-full overflow-hidden p-5 max-w-3xl min-w-xl'
			: 'w-full overflow-hidden p-5 max-w-2xl min-w-xl'}
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
					onChange={(value) => (properties.type = value as StorageAttachType)}
					disabled={properties.diskType === 'filesystem'}
				/>

				<SimpleSelect
					label="Disk Type"
					placeholder="Select Disk Type"
					options={[
						{ value: 'zvol', label: 'ZFS Volume' },
						{ value: 'raw', label: 'Raw Disk' },
						{ value: 'filesystem', label: '9P Filesystem Share' },
						...(properties.type !== 'new' ? [{ value: 'image', label: 'Image' }] : [])
					]}
					bind:value={properties.diskType}
					onChange={(value) => (properties.diskType = value as StorageDiskType)}
				/>

				<SimpleSelect
					label="Pool"
					placeholder="Select Pool"
					options={pools.map((pool) => ({ value: pool.name, label: pool.name }))}
					bind:value={properties.pool}
					onChange={(value) => (properties.pool = value as string)}
					disabled={properties.diskType === 'image' || properties.diskType === 'filesystem'}
				/>
			</div>

			<div class="grid grid-cols-3 gap-4">
				{#if properties.type === 'import'}
					{#if properties.diskType === 'image'}
						<CustomComboBox
							bind:open={imageCombobox.open}
							label={'ISO/Image'}
							bind:value={imageCombobox.value}
							data={images}
							classes="flex-1 space-y-1"
							placeholder="Select ISO/Image"
							width="w-3/4"
							multiple={false}
							shortLabels={true}
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
				{:else if properties.type === 'new' && properties.diskType !== 'image' && properties.diskType !== 'filesystem'}
					<CustomValueInput
						label="Size"
						placeholder={normalizeSizeInputExact(10 * 1024 * 1024 * 1024) ?? '10737418240 B'}
						bind:value={properties.size}
						classes="flex-1 space-y-1"
						onBlur={() => {
							const normalized = normalizeSizeInputExact(properties.size);
							if (normalized !== null) {
								properties.size = normalized;
							}
						}}
					/>
				{:else if properties.type === 'new' && properties.diskType === 'filesystem'}
					<CustomComboBox
						bind:open={filesystemCombobox.open}
						label={'ZFS Filesystem'}
						bind:value={filesystemCombobox.value}
						data={filesystemDatasetOptions}
						classes="flex-1 space-y-1"
						placeholder="Select ZFS filesystem"
						width="w-full"
						multiple={false}
					></CustomComboBox>
				{/if}

				{#if properties.diskType === 'filesystem'}
					<CustomValueInput
						label="Emulation"
						placeholder=""
						value={'virtio-9p'}
						disabled={true}
						classes="flex-1 space-y-1"
					/>
				{:else}
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
							(properties.emulation = value as StorageEmulation)}
					/>
				{/if}

				<CustomValueInput
					label="Boot Order"
					placeholder="2"
					type="number"
					bind:value={properties.bootOrder as number}
					classes="flex-1 space-y-1"
				/>
			</div>

			{#if properties.type === 'new' && properties.diskType === 'filesystem'}
				<div class="mt-3 grid grid-cols-2 gap-4">
					<CustomValueInput
						label="9P Target Name"
						placeholder="shared_dir"
						bind:value={properties.filesystemTarget}
						classes="flex-1 space-y-1"
					/>
					<CustomCheckbox
						label="Read-only share"
						bind:checked={properties.filesystemReadOnly}
						classes="mt-7 flex items-center gap-2"
					/>
				</div>
			{/if}
		{:else}
			<CustomValueInput
				label="Name"
				placeholder="DB Storage"
				bind:value={editProperties.name}
				classes="flex-1 space-y-1"
			/>

			<div class="grid grid-cols-3 gap-4">
				{#if isFilesystemStorageEdit}
					<CustomValueInput
						label="Dataset"
						placeholder=""
						value={selectedStorage?.dataset?.name || '-'}
						disabled={true}
						classes="flex-1 space-y-1"
					/>
				{:else}
					<CustomValueInput
						label={isImageStorageEdit ? 'Size (Read-only)' : 'Size'}
						placeholder={normalizeSizeInputExact(10 * 1024 * 1024 * 1024) ?? '10737418240 B'}
						bind:value={editProperties.size}
						classes="flex-1 space-y-1"
						onBlur={handleEditSizeBlur}
						disabled={isImageStorageEdit}
					/>
				{/if}

				{#if isFilesystemStorageEdit}
					<CustomValueInput
						label="9P Target Name"
						placeholder="shared_dir"
						bind:value={editProperties.filesystemTarget}
						classes="flex-1 space-y-1"
					/>
				{:else}
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
							(editProperties.emulation = value as StorageEmulation)}
					/>
				{/if}

				<CustomValueInput
					label="Boot Order"
					placeholder="2"
					type="number"
					bind:value={editProperties.bootOrder as number}
					classes="flex-1 space-y-1"
				/>
			</div>

			{#if isFilesystemStorageEdit}
				<CustomCheckbox
					label="Read-only share"
					bind:checked={editProperties.filesystemReadOnly}
					classes="mt-3 flex items-center gap-2"
				/>
			{/if}

			<CustomCheckbox
				label="Enabled (Available to VM)"
				bind:checked={editProperties.enable}
				classes="mt-3 flex items-center gap-2"
			/>
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
