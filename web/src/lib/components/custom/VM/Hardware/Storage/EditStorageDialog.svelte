<script lang="ts">
	import { storageUpdate, type StorageUpdateRequest } from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { normalizeSizeInputExact, parseSizeInputToBytes } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { isValid9PTargetName } from '$lib/utils/string';
	import { roundUpToBlock } from '$lib/utils/zfs';
	import { toast } from 'svelte-sonner';
	import {
		mediaEmulationOptions,
		storageSelectClasses,
		writableEmulationOptions,
		type StorageEmulation
	} from './storage-dialog';

	interface Props {
		open: boolean;
		node: string;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
		storageId: number;
		tableData: { rows: Row[]; columns: Column[] } | null;
		reload: boolean;
	}

	let {
		open = $bindable(),
		node,
		datasets,
		downloads,
		vm,
		storageId,
		tableData,
		reload = $bindable()
	}: Props = $props();

	const toastOptions = {
		position: 'bottom-center' as const
	};

	let storages = $derived(vm.storages || []);
	let selectedStorage = $derived(storages.find((storage) => storage.id === storageId) || null);
	let selectedName = $derived.by((): string | null => {
		const storage = tableData?.rows.find((row) => row.id === storageId);
		return typeof storage?.name === 'string' ? storage.name : null;
	});
	let selectedStorageDisplaySize = $derived.by(() => {
		const tableStorage = tableData?.rows.find((row) => row.id === storageId) || null;
		const tableSize = Number(tableStorage?.size);
		return Number.isFinite(tableSize) && tableSize > 0 ? tableSize : selectedStorage?.size || 0;
	});
	let selectedStorageType = $derived(selectedStorage?.type ?? null);
	let isImageStorageEdit = $derived(selectedStorageType === 'image');
	let isFilesystemStorageEdit = $derived(selectedStorageType === 'filesystem');
	let selectedStorageTitle = $derived.by(() => {
		const storedName = selectedStorage?.name?.trim();
		if (storedName) return storedName;
		if (selectedStorage?.type === 'image') return 'Installation media';
		if (selectedStorage?.type === 'filesystem') {
			return selectedStorage.filesystemTarget?.trim() || 'Filesystem share';
		}
		return selectedName || 'Storage';
	});
	let selectedStorageSourceName = $derived.by(() => {
		if (!selectedStorage) return '';
		if (selectedStorage.type === 'image') {
			return (
				downloads.find((download) => download.uuid === selectedStorage.uuid)?.name ||
				'Unknown media'
			);
		}
		return (
			selectedStorage.dataset?.name ||
			selectedStorage.pool ||
			(selectedStorage.type === 'filesystem' ? 'Filesystem share' : 'Managed storage')
		);
	});
	let usedBootOrders = $derived.by(() => {
		const used: number[] = [];
		for (const storage of vm.storages) {
			if (storage.type === 'filesystem' || storage.id === storageId) continue;
			if (storage.bootOrder === 0 || storage.bootOrder) used.push(storage.bootOrder);
		}
		return used;
	});
	let editEmulationOptions = $derived(
		isImageStorageEdit ? mediaEmulationOptions : writableEmulationOptions
	);

	interface EditProperties {
		name: string;
		size: string;
		emulation: StorageEmulation;
		filesystemTarget: string;
		filesystemReadOnly: boolean;
		bootOrder: number | null;
		loading: boolean;
	}

	function createEditOptions(): EditProperties {
		let emulation = selectedStorage?.emulation ?? 'ahci-hd';
		if (selectedStorage?.type === 'filesystem') {
			emulation = 'virtio-9p';
		} else if (
			selectedStorage?.type !== 'image' &&
			(emulation === 'ahci-cd' || emulation === 'virtio-9p')
		) {
			emulation = 'ahci-hd';
		}

		return {
			name: selectedStorage?.name?.trim() || selectedName || '',
			size: selectedStorage ? (normalizeSizeInputExact(selectedStorageDisplaySize) ?? '') : '',
			emulation,
			filesystemTarget: selectedStorage?.filesystemTarget ?? '',
			filesystemReadOnly: selectedStorage?.readOnly ?? false,
			bootOrder: selectedStorage?.bootOrder ?? 0,
			loading: false
		};
	}

	let editProperties = $state(createEditOptions());

	function handleEditSizeBlur() {
		if (isImageStorageEdit || isFilesystemStorageEdit || editProperties.size.trim() === '') return;
		const parsed = parseSizeInputToBytes(editProperties.size);
		if (parsed === null) return;
		if (selectedStorage && parsed < selectedStorage.size - 1024) {
			editProperties.size = normalizeSizeInputExact(selectedStorage.size) ?? '0 B';
			toast.error('New size cannot be smaller than current size', toastOptions);
			return;
		}
		const normalized = normalizeSizeInputExact(parsed);
		if (normalized !== null) editProperties.size = normalized;
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

		let parsedSize: number | undefined;
		if (!isImageStorageEdit && !isFilesystemStorageEdit) {
			const parsed = parseSizeInputToBytes(editProperties.size);
			if (parsed === null || parsed <= 0) {
				toast.error('Invalid size format', toastOptions);
				return;
			}
			parsedSize = parsed;
			if (parsedSize < selectedStorage.size - 1024) {
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

		let roundedSize: number | undefined;
		if (parsedSize !== undefined) {
			const dataset = selectedStorage.dataset;
			const foundDataset = datasets.find((candidate) => candidate.guid === dataset?.guid);
			let blockSize = 8192;
			if (foundDataset?.properties) {
				if (
					foundDataset.type === GZFSDatasetTypeSchema.enum.VOLUME &&
					foundDataset.properties.volblocksize
				) {
					const value = Number(foundDataset.properties.volblocksize);
					if (Number.isFinite(value) && value > 0) blockSize = value;
				} else if (
					foundDataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM &&
					foundDataset.properties.recordsize
				) {
					const value = Number(foundDataset.properties.recordsize);
					if (Number.isFinite(value) && value > 0) blockSize = value;
				}
			}
			roundedSize = roundUpToBlock(parsedSize, blockSize);
		}

		const request: StorageUpdateRequest = {
			name,
			emulation: isFilesystemStorageEdit ? 'virtio-9p' : editProperties.emulation,
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
		class="max-h-[calc(100vh-2rem)] w-full gap-2 overflow-y-auto p-5 sm:max-w-xl"
		showResetButton={Boolean(selectedStorage)}
		onReset={() => {
			if (selectedStorage) editProperties = createEditOptions();
		}}
		onClose={() => {
			if (selectedStorage) editProperties = createEditOptions();
			open = false;
		}}
	>
		<Dialog.Header class="pr-14">
			<Dialog.Title class="flex min-w-0 items-center gap-2">
				<SpanWithIcon
					icon="icon-[grommet-icons--storage]"
					size="h-5 w-5"
					gap="gap-2"
					title={'Edit - ' + selectedStorageTitle}
				/>
			</Dialog.Title>
			<Dialog.Description class="text-left">
				<span class="break-words" title={selectedStorageSourceName}>
					{selectedStorageSourceName}
				</span>
			</Dialog.Description>
		</Dialog.Header>

		{#if selectedStorage}
			<div class="space-y-4">
				<CustomValueInput
					label="Name"
					placeholder="DB Storage"
					bind:value={editProperties.name}
					classes="space-y-1"
				/>

				<div
					class={'grid gap-4 ' + (isFilesystemStorageEdit ? 'sm:grid-cols-2' : 'sm:grid-cols-3')}
				>
					{#if isFilesystemStorageEdit}
						<CustomValueInput
							label="Dataset"
							placeholder=""
							value={selectedStorage.dataset?.name || '-'}
							disabled={true}
							classes="space-y-1"
						/>
					{:else}
						<CustomValueInput
							label={isImageStorageEdit ? 'Size � read-only' : 'Size'}
							placeholder="10 GiB"
							bind:value={editProperties.size}
							classes="space-y-1"
							onBlur={handleEditSizeBlur}
							disabled={isImageStorageEdit}
						/>
					{/if}

					{#if isFilesystemStorageEdit}
						<CustomValueInput
							label="9P target name"
							placeholder="shared_dir"
							bind:value={editProperties.filesystemTarget}
							classes="space-y-1"
						/>
					{:else}
						<SimpleSelect
							label="Emulation"
							placeholder="Select emulation"
							options={editEmulationOptions}
							bind:value={editProperties.emulation}
							onChange={(value) => (editProperties.emulation = value as StorageEmulation)}
							classes={storageSelectClasses}
						/>
					{/if}

					{#if !isFilesystemStorageEdit}
						<CustomValueInput
							label="Boot order"
							placeholder="0"
							type="number"
							bind:value={editProperties.bootOrder as number}
							classes="space-y-1"
						/>
					{/if}
				</div>

				{#if isFilesystemStorageEdit}
					<CustomCheckbox
						label="Read-only share"
						bind:checked={editProperties.filesystemReadOnly}
						classes="flex items-center gap-2"
					/>
				{/if}

				<Dialog.Footer>
					<Button
						size="sm"
						type="button"
						class="h-9 min-w-36"
						onclick={update}
						disabled={editProperties.loading}
					>
						{#if editProperties.loading}
							<span class="icon-[eos-icons--loading] mr-2 size-4 animate-spin"></span>
							Saving...
						{:else}
							Save changes
						{/if}
					</Button>
				</Dialog.Footer>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
