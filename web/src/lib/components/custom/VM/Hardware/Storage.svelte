<script lang="ts">
	import { getFiles } from '$lib/api/system/file-explorer';
	import {
		storageAttach,
		storageUpdate,
		type StorageAttachRequest,
		type StorageUpdateRequest
	} from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { normalizeSizeInputExact, parseSizeInputToBytes } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
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
		node: string;
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
		node,
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

	let selectedName = $derived.by((): string | null => {
		if (storageId === null) return null;

		const storage = tableData?.rows.find((s) => s.id === storageId);
		return typeof storage?.name === 'string' ? storage.name : null;
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

	function createAttachOptions() {
		return {
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
	}

	interface EditProperties {
		name: string;
		size: string;
		emulation: StorageEmulation;
		filesystemTarget: string;
		filesystemReadOnly: boolean;
		enable: boolean;
		bootOrder: number | null;
		loading: boolean;
	}

	function createEditOptions(): EditProperties {
		return {
			name: selectedStorage?.name ?? selectedName ?? '',
			size: selectedStorage ? (normalizeSizeInputExact(selectedStorageDisplaySize) ?? '') : '',
			emulation:
				selectedStorage?.type === 'filesystem'
					? 'virtio-9p'
					: (selectedStorage?.emulation ?? 'ahci-hd'),
			filesystemTarget: selectedStorage?.filesystemTarget ?? '',
			filesystemReadOnly: selectedStorage?.readOnly ?? false,
			enable: selectedStorage?.enable ?? true,
			bootOrder: selectedStorage?.bootOrder ?? 0,
			loading: false
		};
	}

	let properties = $state(createAttachOptions());
	let editProperties = $state(createEditOptions());

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
			if (storage.type === 'filesystem') {
				continue;
			}

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

	function setDiskType(value: string) {
		properties.diskType = value as StorageDiskType;
		if (properties.diskType === 'filesystem') {
			properties.type = 'new';
			properties.emulation = 'virtio-9p';
		} else if (properties.emulation === 'virtio-9p') {
			properties.emulation = 'ahci-hd';
		}
		if (properties.diskType === 'image') {
			properties.pool = '';
			properties.rawPath = '';
			zvolCombobox.value = '';
			filesystemCombobox.value = '';
		}
	}

	function setAttachType(value: string) {
		properties.type = value as StorageAttachType;
		if (properties.type === 'new' && properties.diskType === 'image') {
			setDiskType('zvol');
		}
	}

	let filesystemDatasetOptions = $derived.by(() =>
		datasets
			.filter((dataset) => dataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM)
			.map((dataset) => ({
				value: dataset.guid || dataset.name,
				label: `${dataset.name} (${dataset.mountpoint})`
			}))
	);

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

	function resetAttachForm() {
		properties = createAttachOptions();
		zvolCombobox = { open: false, value: '' };
		imageCombobox = { open: false, value: '' };
		filesystemCombobox = { open: false, value: '' };
	}

	async function attach() {
		const name = properties.name.trim();
		if (name === '' || name.length > 128) {
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
		if (properties.diskType !== 'filesystem') {
			const bootOrder = Number(properties.bootOrder);
			if (!Number.isInteger(bootOrder) || bootOrder < 0) {
				toast.error('Please specify a valid boot order', toastOptions);
				return;
			}
			if (usedBootOrders.includes(bootOrder)) {
				toast.error('Boot order already in use', toastOptions);
				return;
			}
		}

		properties.loading = true;
		try {
			let request: StorageAttachRequest;
			const emulation = properties.emulation as Exclude<StorageEmulation, 'virtio-9p'>;
			if (properties.type === 'import') {
				if (properties.diskType === 'filesystem') {
					toast.error('Filesystem storage cannot be imported', toastOptions);
					return;
				}
				if (properties.diskType === 'raw') {
					const rawPath = properties.rawPath.trim();
					if (!isValidAbsPath(rawPath)) {
						toast.error('Invalid disk path', toastOptions);
						return;
					}
					const files = await getFiles(getPathParent(rawPath), node);
					if (isAPIResponse(files)) {
						handleAPIError(files);
						return;
					}
					if (!files.some((file) => file.id === rawPath)) {
						toast.error('Unable to find disk', toastOptions);
						return;
					}
					request = {
						attachType: 'import',
						storageType: 'raw',
						name,
						rawPath,
						pool: properties.pool,
						emulation,
						bootOrder: Number(properties.bootOrder)
					};
				} else if (properties.diskType === 'zvol') {
					if (!zvolCombobox.value) {
						toast.error('Please select a ZFS Volume', toastOptions);
						return;
					}
					request = {
						attachType: 'import',
						storageType: 'zvol',
						name,
						dataset: zvolCombobox.value,
						pool: properties.pool,
						emulation,
						bootOrder: Number(properties.bootOrder)
					};
				} else {
					if (!imageCombobox.value) {
						toast.error('Please select an ISO/Image', toastOptions);
						return;
					}
					request = {
						attachType: 'import',
						storageType: 'image',
						name,
						downloadUUID: imageCombobox.value,
						emulation,
						bootOrder: Number(properties.bootOrder)
					};
				}
			} else if (properties.diskType === 'filesystem') {
				const target = properties.filesystemTarget.trim();
				if (!filesystemCombobox.value) {
					toast.error('Please select a ZFS filesystem dataset', toastOptions);
					return;
				}
				if (!isValid9PTargetName(target)) {
					toast.error(
						"Invalid 9P target name (letters, numbers, '.', '_' and '-' only)",
						toastOptions
					);
					return;
				}
				request = {
					attachType: 'new',
					storageType: 'filesystem',
					name,
					dataset: filesystemCombobox.value,
					filesystemTarget: target,
					readOnly: properties.filesystemReadOnly,
					emulation: 'virtio-9p'
				};
			} else {
				if (properties.diskType === 'image') {
					toast.error('Images must be imported', toastOptions);
					return;
				}
				const parsedSize = parseSizeInputToBytes(properties.size);
				if (parsedSize === null || parsedSize <= 0) {
					toast.error('Invalid size format', toastOptions);
					return;
				}
				request = {
					attachType: 'new',
					storageType: properties.diskType as 'raw' | 'zvol',
					name,
					size: parsedSize,
					pool: properties.pool,
					emulation,
					bootOrder: Number(properties.bootOrder)
				};
			}

			const response = await storageAttach(vm.rid, request, { hostname: node });
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error(
					properties.type === 'import' ? 'Failed to import storage' : 'Failed to attach storage',
					toastOptions
				);
				return;
			}

			toast.success(
				properties.type === 'import' ? 'Storage imported' : 'Storage attached',
				toastOptions
			);
			reload = true;
			resetAttachForm();
			open = false;
		} catch {
			toast.error('Failed to attach storage', toastOptions);
		} finally {
			properties.loading = false;
		}
	}

	async function update() {
		if (!selectedStorage) {
			toast.error('Unable to find storage', toastOptions);
			return;
		}
		const name = editProperties.name.trim();
		if (name === '' || name.length > 128) {
			toast.error('Invalid storage name', toastOptions);
			return;
		}

		let parsedSize: number | undefined = undefined;
		if (!isImageStorageEdit && !isFilesystemStorageEdit) {
			const parsed = parseSizeInputToBytes(editProperties.size);
			if (parsed === null || parsed <= 0) {
				toast.error('Invalid size format', toastOptions);
				return;
			}
			parsedSize = parsed;

			const EPSILON = 1024;
			if (parsedSize < selectedStorage.size - EPSILON) {
				toast.error('New size cannot be smaller than current size', toastOptions);
				return;
			}
		}

		if (
			isFilesystemStorageEdit &&
			!isValid9PTargetName((editProperties.filesystemTarget || '').trim())
		) {
			toast.error("Invalid 9P target name (letters, numbers, '.', '_' and '-' only)", toastOptions);
			return;
		}

		if (!isFilesystemStorageEdit) {
			const bootOrder = Number(editProperties.bootOrder);
			if (!Number.isInteger(bootOrder) || bootOrder < 0) {
				toast.error('Please specify a valid boot order', toastOptions);
				return;
			}

			if (usedBootOrders.includes(bootOrder)) {
				toast.error('Boot order already in use', toastOptions);
				return;
			}
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
					const value = Number(fDataset.properties.volblocksize);
					if (Number.isFinite(value) && value > 0) blockSize = value;
				} else if (
					fDataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM &&
					fDataset.properties.recordsize
				) {
					const value = Number(fDataset.properties.recordsize);
					if (Number.isFinite(value) && value > 0) blockSize = value;
				}
			}

			roundedSize = roundUpToBlock(parsedSize, blockSize);
		}

		const request: StorageUpdateRequest = {
			name,
			emulation: isFilesystemStorageEdit ? 'virtio-9p' : editProperties.emulation,
			enable: editProperties.enable,
			...(roundedSize !== undefined ? { size: roundedSize } : {}),
			...(!isFilesystemStorageEdit ? { bootOrder: Number(editProperties.bootOrder) } : {}),
			...(isFilesystemStorageEdit
				? {
						filesystemTarget: editProperties.filesystemTarget.trim(),
						readOnly: editProperties.filesystemReadOnly
					}
				: {})
		};

		editProperties.loading = true;
		try {
			const response = await storageUpdate(vm.rid, selectedStorage.id, request, {
				hostname: node
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to update storage', toastOptions);
				return;
			}

			toast.success('Storage updated', toastOptions);
			reload = true;
			editProperties = createEditOptions();
			open = false;
		} catch {
			toast.error('Failed to update storage', toastOptions);
		} finally {
			editProperties.loading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class={selectedStorage
			? 'w-full overflow-hidden p-5 max-w-3xl min-w-xl'
			: 'w-full overflow-hidden p-5 max-w-2xl min-w-xl'}
		showResetButton={true}
		onReset={() => {
			if (selectedStorage) {
				editProperties = createEditOptions();
			} else {
				resetAttachForm();
			}
		}}
		onClose={() => {
			if (selectedStorage) {
				editProperties = createEditOptions();
			} else {
				resetAttachForm();
			}
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[grommet-icons--storage]"
					size="h-5 w-5"
					gap="gap-2"
					title={selectedName ? `Edit - ${selectedName}` : 'New Storage'}
				/>
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
					onChange={setAttachType}
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
					onChange={setDiskType}
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
							label="ISO/Image"
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
							label="ZFS Volume"
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
						label="ZFS Filesystem"
						bind:value={filesystemCombobox.value}
						data={filesystemDatasetOptions}
						classes="flex-1 space-y-1"
						placeholder="Select ZFS filesystem"
						width="w-1/2"
						multiple={false}
					></CustomComboBox>
				{/if}

				{#if properties.diskType === 'filesystem'}
					<CustomValueInput
						label="Emulation"
						placeholder=""
						value="virtio-9p"
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
						onChange={(value) => (properties.emulation = value as StorageEmulation)}
						classes={{
							parent: 'flex-1 space-y-1',
							label: 'flex h-7 items-center whitespace-nowrap text-sm',
							trigger:
								'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
						}}
					/>
				{/if}

				{#if properties.diskType !== 'filesystem'}
					<CustomValueInput
						label="Boot Order"
						placeholder="2"
						type="number"
						bind:value={properties.bootOrder as number}
						classes="flex-1 space-y-1"
					/>
				{:else}
					<CustomValueInput
						label="9P Target Name"
						placeholder="shared_dir"
						bind:value={properties.filesystemTarget}
						classes="flex-1 space-y-1"
					/>
				{/if}
			</div>

			{#if properties.type === 'new' && properties.diskType === 'filesystem'}
				<div class="mt-[-1] grid grid-cols-1 gap-2">
					<CustomCheckbox
						label="Read-only share"
						bind:checked={properties.filesystemReadOnly}
						classes="mt-1 flex items-center gap-2"
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

			<div class={`grid gap-4 ${isFilesystemStorageEdit ? 'grid-cols-2' : 'grid-cols-3'}`}>
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
						onChange={(value) => (editProperties.emulation = value as StorageEmulation)}
						classes={{
							parent: 'flex-1 space-y-1',
							label: 'flex h-7 items-center whitespace-nowrap text-sm',
							trigger:
								'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
						}}
					/>
				{/if}

				{#if !isFilesystemStorageEdit}
					<CustomValueInput
						label="Boot Order"
						placeholder="2"
						type="number"
						bind:value={editProperties.bootOrder as number}
						classes="flex-1 space-y-1"
					/>
				{/if}
			</div>

			{#if isFilesystemStorageEdit}
				<div class="mt-3 flex items-center gap-6">
					<CustomCheckbox
						label="Read-only share"
						bind:checked={editProperties.filesystemReadOnly}
						classes="flex items-center gap-2"
					/>
					<CustomCheckbox
						label="Enabled (Available to VM)"
						bind:checked={editProperties.enable}
						classes="flex items-center gap-2"
					/>
				</div>
			{:else}
				<CustomCheckbox
					label="Enabled (Available to VM)"
					bind:checked={editProperties.enable}
					classes="mt-3 flex items-center gap-2"
				/>
			{/if}
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
						<span>{selectedStorage ? 'Saving...' : 'Attaching...'}</span>
					{:else}
						<span>{selectedStorage ? 'Save Changes' : 'Attach Storage'}</span>
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
