<script lang="ts">
	import { getFiles } from '$lib/api/system/file-explorer';
	import {
		createStorageFromImage,
		storageAttach,
		type StorageAttachRequest,
		type StorageFromImageRequest
	} from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { normalizeSizeInputExact, parseSizeInputToBytes } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { getPathParent, isValid9PTargetName, isValidAbsPath } from '$lib/utils/string';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import OperationPreview from './OperationPreview.svelte';
	import StorageChoiceStep from './StorageChoiceStep.svelte';
	import {
		backingLabel,
		backingOptions,
		createAttachOptions,
		deriveNameFromImage,
		intentDetails,
		mediaEmulationOptions,
		storageSelectClasses,
		writableEmulationOptions,
		type OperationPreviewData,
		type StorageEmulation,
		type StorageIntent,
		type WritableStorageEmulation
	} from './storage-dialog';

	interface Props {
		open: boolean;
		node: string;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
		vms: VM[];
		pools: Zpool[];
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
		reload = $bindable()
	}: Props = $props();

	const toastOptions = {
		position: 'bottom-center' as const
	};

	let intent = $state<StorageIntent | null>(null);
	let properties = $state(createAttachOptions(''));
	let suggestedImageName = $state('');
	let imageImportController: AbortController | null = null;

	let zvolCombobox = $state({ open: false, value: '' });
	let imageCombobox = $state({ open: false, value: '' });
	let filesystemCombobox = $state({ open: false, value: '' });

	let allImages = $derived(getISOs(downloads, true));
	let writableImages = $derived(allImages.filter((image) => image.kind === 'disk'));
	let imageOptions = $derived(intent === 'from-image' ? writableImages : allImages);
	let selectedImageOption = $derived(
		allImages.find((image) => image.value === imageCombobox.value) ?? null
	);
	let selectedImageIsOptical = $derived(selectedImageOption?.kind === 'iso');
	let imageWriteInProgress = $derived(properties.loading && intent === 'from-image');

	$effect(() => {
		if (!imageWriteInProgress) return;
		const handleBeforeUnload = (event: BeforeUnloadEvent) => {
			event.preventDefault();
			event.returnValue = '';
		};
		const handlePageHide = () => imageImportController?.abort();
		window.addEventListener('beforeunload', handleBeforeUnload);
		window.addEventListener('pagehide', handlePageHide);
		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			window.removeEventListener('pagehide', handlePageHide);
		};
	});

	onDestroy(() => imageImportController?.abort());
	let usedDatasets = $derived.by(() => {
		const used: string[] = [];
		for (const candidateVM of vms) {
			for (const storage of candidateVM.storages) {
				if (storage.dataset?.guid) used.push(storage.dataset.guid);
			}
		}
		return used;
	});
	let usedBootOrders = $derived.by(() => {
		const used: number[] = [];
		for (const storage of vm.storages) {
			if (storage.type === 'filesystem') continue;
			if (storage.bootOrder === 0 || storage.bootOrder) used.push(storage.bootOrder);
		}
		return used;
	});
	let filesystemDatasetOptions = $derived.by(() =>
		datasets
			.filter((dataset) => dataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM)
			.map((dataset) => ({
				value: dataset.guid || dataset.name,
				label: dataset.name + ' (' + dataset.mountpoint + ')'
			}))
	);
	let availableZvolOptions = $derived.by(() =>
		datasets
			.filter(
				(dataset) =>
					dataset.type === GZFSDatasetTypeSchema.enum.VOLUME &&
					!usedDatasets.some((used) => used === dataset.guid)
			)
			.map((dataset) => ({
				value: dataset.guid || dataset.name,
				label: dataset.name
			}))
	);
	let selectedFilesystem = $derived(
		datasets.find(
			(dataset) =>
				dataset.guid === filesystemCombobox.value || dataset.name === filesystemCombobox.value
		) ?? null
	);
	let selectedZvol = $derived(
		datasets.find(
			(dataset) => dataset.guid === zvolCombobox.value || dataset.name === zvolCombobox.value
		) ?? null
	);
	let selectedIntent = $derived(intent ? intentDetails[intent] : null);

	function nextBootOrder(): number {
		let candidate = 0;
		while (usedBootOrders.includes(candidate)) candidate += 1;
		return candidate;
	}

	function setIntent(nextIntent: StorageIntent) {
		intent = nextIntent;
		if (!properties.pool) properties.pool = pools[0]?.name ?? '';
		if (properties.bootOrder === null) properties.bootOrder = nextBootOrder();

		if (nextIntent === 'empty' || nextIntent === 'from-image') {
			if (nextIntent === 'from-image' && selectedImageIsOptical) {
				imageCombobox.value = '';
				if (properties.name === suggestedImageName) properties.name = '';
				suggestedImageName = '';
			}
			if (properties.diskType !== 'raw' && properties.diskType !== 'zvol') {
				properties.diskType = 'zvol';
			}
			if (properties.emulation === 'ahci-cd' || properties.emulation === 'virtio-9p') {
				properties.emulation = 'nvme';
			}
		} else if (nextIntent === 'media') {
			properties.diskType = 'image';
			properties.emulation = selectedImageIsOptical ? 'ahci-cd' : 'nvme';
		} else if (nextIntent === 'filesystem') {
			properties.diskType = 'filesystem';
			properties.emulation = 'virtio-9p';
		} else {
			if (properties.diskType !== 'raw' && properties.diskType !== 'zvol') {
				properties.diskType = 'raw';
			}
			if (properties.emulation === 'ahci-cd' || properties.emulation === 'virtio-9p') {
				properties.emulation = 'nvme';
			}
		}
	}

	function goBack() {
		intent = null;
		zvolCombobox.open = false;
		imageCombobox.open = false;
		filesystemCombobox.open = false;
	}

	function resetAttachForm() {
		intent = null;
		properties = createAttachOptions(pools[0]?.name ?? '');
		suggestedImageName = '';
		zvolCombobox = { open: false, value: '' };
		imageCombobox = { open: false, value: '' };
		filesystemCombobox = { open: false, value: '' };
	}

	function handleImageSelection(value: string | string[]) {
		if (typeof value !== 'string') return;
		imageCombobox.value = value;
		const option = allImages.find((image) => image.value === value);
		if (!option) return;

		const nextSuggestion = deriveNameFromImage(option.label);
		if (!properties.name.trim() || properties.name === suggestedImageName) {
			properties.name = nextSuggestion;
		}
		suggestedImageName = nextSuggestion;

		if (intent === 'media') {
			properties.emulation = option.kind === 'iso' ? 'ahci-cd' : 'nvme';
		}
	}

	function setBacking(value: string) {
		properties.diskType = value as 'raw' | 'zvol';
	}

	let preview = $derived.by((): OperationPreviewData => {
		const managedResult = `Writable ${backingLabel(properties.diskType)}`;

		switch (intent) {
			case 'empty':
				return {
					source: 'Fresh allocation',
					action: 'allocate',
					result: managedResult,
					note: 'Sylve owns and manages the new backing storage for this VM.',
					warning: false
				};
			case 'from-image':
				return {
					source: selectedImageOption?.label ?? 'Choose an image',
					action: 'copy / convert',
					result: managedResult,
					note: 'The Downloader source stays unchanged. Sylve creates a separate managed, writable disk.',
					warning: false
				};
			case 'media':
				return {
					source: selectedImageOption?.label ?? 'Choose an image',
					action: 'reference',
					result: `Read-only ${selectedImageIsOptical ? 'CD' : 'disk'}`,
					note: 'The source stays in Downloader and can be reused after this media is ejected.',
					warning: false
				};
			case 'filesystem':
				return {
					source: selectedFilesystem?.name ?? 'Choose a filesystem',
					action: 'share',
					result: `${properties.filesystemReadOnly ? 'Read-only' : 'Writable'} 9P share`,
					note: 'The ZFS dataset is shared in place; Sylve does not copy or take ownership of it.',
					warning: false
				};
			case 'advanced': {
				const importsZvol = properties.diskType === 'zvol';
				return {
					source: importsZvol
						? (selectedZvol?.name ?? 'Choose a ZFS volume')
						: properties.rawPath.trim() || 'Enter a host path',
					action: importsZvol ? 'move / copy' : 'copy',
					result: managedResult,
					note: importsZvol
						? "A same-pool volume is moved into Sylve's managed namespace. A cross-pool volume is copied."
						: 'The host file remains unchanged after it is copied into managed storage.',
					warning: importsZvol
				};
			}
			default:
				return { source: '', action: '', result: '', note: '', warning: false };
		}
	});

	function validateBootOrder(): number | null {
		const bootOrder = Number(properties.bootOrder);
		if (!Number.isInteger(bootOrder) || bootOrder < 0) {
			toast.error('Please specify a valid boot order', toastOptions);
			return null;
		}
		if (usedBootOrders.includes(bootOrder)) {
			toast.error('Boot order already in use', toastOptions);
			return null;
		}
		return bootOrder;
	}

	async function attach() {
		if (!intent) return;

		const name = properties.name.trim();
		if (name === '' || name.length > 128) {
			toast.error('Invalid storage name', toastOptions);
			return;
		}
		if (
			(intent === 'empty' || intent === 'from-image' || intent === 'advanced') &&
			!properties.pool
		) {
			toast.error('No ZFS pool selected', toastOptions);
			return;
		}
		const bootOrder = intent === 'filesystem' ? null : validateBootOrder();
		if (intent !== 'filesystem' && bootOrder === null) return;

		const operationIntent = intent;
		properties.loading = true;
		try {
			let response: Awaited<ReturnType<typeof storageAttach>>;

			if (operationIntent === 'from-image') {
				if (!imageCombobox.value) {
					toast.error('Please select a disk image', toastOptions);
					return;
				}
				let targetSize: number | undefined;
				if (properties.size.trim()) {
					const parsedSize = parseSizeInputToBytes(properties.size);
					if (parsedSize === null || parsedSize <= 0) {
						toast.error('Invalid target size', toastOptions);
						return;
					}
					targetSize = parsedSize;
				}
				const request: StorageFromImageRequest = {
					downloadUUID: imageCombobox.value,
					name,
					pool: properties.pool,
					storageType: properties.diskType as 'raw' | 'zvol',
					emulation: properties.emulation as WritableStorageEmulation,
					bootOrder: bootOrder ?? undefined,
					...(targetSize !== undefined ? { size: targetSize } : {})
				};
				imageImportController = new AbortController();
				response = await createStorageFromImage(vm.rid, request, {
					hostname: node,
					signal: imageImportController.signal
				});
			} else {
				let request: StorageAttachRequest;

				if (operationIntent === 'empty') {
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
						emulation: properties.emulation as WritableStorageEmulation,
						bootOrder: bootOrder ?? undefined
					};
				} else if (operationIntent === 'media') {
					if (!imageCombobox.value) {
						toast.error('Please select an ISO or disk image', toastOptions);
						return;
					}
					request = {
						attachType: 'import',
						storageType: 'image',
						name,
						downloadUUID: imageCombobox.value,
						emulation: properties.emulation as Exclude<StorageEmulation, 'virtio-9p'>,
						bootOrder: bootOrder ?? undefined
					};
				} else if (operationIntent === 'filesystem') {
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
				} else if (properties.diskType === 'raw') {
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
						emulation: properties.emulation as WritableStorageEmulation,
						bootOrder: bootOrder ?? undefined
					};
				} else {
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
						emulation: properties.emulation as WritableStorageEmulation,
						bootOrder: bootOrder ?? undefined
					};
				}
				response = await storageAttach(vm.rid, request, { hostname: node });
			}

			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error(
					operationIntent === 'from-image'
						? 'Failed to create disk from image'
						: 'Failed to add storage',
					toastOptions
				);
				return;
			}

			const successMessage: Record<StorageIntent, string> = {
				empty: 'Disk created',
				'from-image': 'Writable disk created',
				media: 'Media attached',
				filesystem: 'Filesystem shared',
				advanced: 'Storage imported'
			};
			toast.success(successMessage[operationIntent], toastOptions);
			reload = true;
			resetAttachForm();
			open = false;
		} catch (error) {
			if (!isRequestCancellation(error)) {
				toast.error('Failed to add storage', toastOptions);
			}
		} finally {
			imageImportController = null;
			properties.loading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[calc(100vh-2rem)] w-full overflow-y-auto p-5 sm:max-w-3xl"
		showCloseButton={!imageWriteInProgress}
		onEscapeKeydown={(event) => {
			if (imageWriteInProgress) event.preventDefault();
		}}
		onClose={() => {
			if (imageWriteInProgress) return;
			resetAttachForm();
			open = false;
		}}
	>
		<Dialog.Header class="pr-14">
			<Dialog.Title class="flex min-w-0 items-center gap-2">
				{#if selectedIntent}
					<button
						type="button"
						class="-ml-1 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50"
						onclick={goBack}
						disabled={properties.loading}
						aria-label="Back to storage choices"
						title="Back to choices"
					>
						<span class="icon-[lucide--arrow-left] size-4"></span>
						<span class="sr-only">Back to choices</span>
					</button>
					<span class="truncate">{selectedIntent.title}</span>
				{:else}
					<SpanWithIcon
						icon="icon-[lucide--circle-plus]"
						size="h-5 w-5"
						gap="gap-2"
						title="Add storage"
					/>
				{/if}
			</Dialog.Title>
			<Dialog.Description class={selectedIntent ? 'sr-only' : 'text-left'}>
				{#if selectedIntent}
					Configure storage for this virtual machine.
				{:else}
					Choose the operation you want. The next screen only asks for what it needs.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		{#if intent === null}
			<StorageChoiceStep onSelect={setIntent} />
		{:else if selectedIntent}
			<div class="space-y-3">
				<div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_16rem] lg:items-stretch">
					<section class="min-w-0 rounded-xl border bg-card p-4 shadow-sm lg:h-full">
						<div class="flex h-8 min-w-0 items-center">
							<p class="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
								Configuration
							</p>
						</div>

						<div class="mt-3 grid min-w-0 gap-x-4 gap-y-3 sm:grid-cols-2">
							{#if intent === 'from-image' || intent === 'media'}
								<div class="min-w-0 sm:col-span-2">
									<CustomComboBox
										bind:open={imageCombobox.open}
										label={intent === 'from-image' ? 'Source disk image' : 'Downloader media'}
										bind:value={imageCombobox.value}
										onValueChange={handleImageSelection}
										data={imageOptions}
										classes="space-y-1"
										placeholder="Choose a completed download"
										width="w-[min(34rem,calc(100vw-3rem))]"
										multiple={false}
										disallowEmpty={true}
										shortLabels={true}
									></CustomComboBox>
									{#if imageOptions.length === 0}
										<p class="mt-1.5 text-xs text-muted-foreground">
											No compatible completed downloads are available.
										</p>
									{:else if imageOptions.every((image) => image.disabled)}
										<p class="mt-1.5 text-xs text-muted-foreground">
											Compressed images must be extracted before they can be used here.
										</p>
									{/if}
								</div>
							{/if}

							{#if intent === 'empty' || intent === 'from-image'}
								<CustomValueInput
									label="Name"
									placeholder="router-disk"
									bind:value={properties.name}
									classes="min-w-0 space-y-1"
								/>
								<CustomValueInput
									label={intent === 'from-image' ? 'Target size · optional' : 'Size'}
									placeholder={intent === 'from-image' ? 'Auto from image' : '10 GiB'}
									hint={intent === 'from-image'
										? 'Leave blank to use the image virtual size. A larger disk does not automatically grow guest partitions.'
										: 'Examples: 10 GiB, 512 MiB, or an exact byte count.'}
									bind:value={properties.size}
									classes="min-w-0 space-y-1"
									onBlur={() => {
										if (!properties.size.trim()) return;
										const normalized = normalizeSizeInputExact(properties.size);
										if (normalized !== null) properties.size = normalized;
									}}
								/>
								<SimpleSelect
									label="Backing"
									placeholder="Select backing"
									options={backingOptions}
									bind:value={properties.diskType}
									onChange={setBacking}
									classes={storageSelectClasses}
								/>
								<SimpleSelect
									label="Pool"
									placeholder="Select pool"
									options={pools.map((pool) => ({ value: pool.name, label: pool.name }))}
									bind:value={properties.pool}
									onChange={(value) => (properties.pool = value)}
									classes={storageSelectClasses}
								/>
								<SimpleSelect
									label="Emulation"
									placeholder="Select emulation"
									options={writableEmulationOptions}
									bind:value={properties.emulation}
									onChange={(value) => (properties.emulation = value as StorageEmulation)}
									classes={storageSelectClasses}
								/>
								<CustomValueInput
									label="Boot order"
									placeholder="0"
									type="number"
									bind:value={properties.bootOrder as number}
									classes="min-w-0 space-y-1"
								/>
							{:else if intent === 'media'}
								<CustomValueInput
									label="Name"
									placeholder="FreeBSD installer"
									bind:value={properties.name}
									classes="min-w-0 space-y-1 sm:col-span-2"
								/>
								<SimpleSelect
									label="Emulation"
									placeholder="Select emulation"
									options={mediaEmulationOptions}
									bind:value={properties.emulation}
									onChange={(value) => (properties.emulation = value as StorageEmulation)}
									classes={storageSelectClasses}
								/>
								<CustomValueInput
									label="Boot order"
									placeholder="0"
									type="number"
									bind:value={properties.bootOrder as number}
									classes="min-w-0 space-y-1"
								/>
								<div
									class="flex items-start gap-2.5 rounded-md border bg-muted/30 p-3 text-sm sm:col-span-2"
								>
									<span
										class="icon-[lucide--lock-keyhole] mt-0.5 size-4 shrink-0 text-muted-foreground"
									></span>
									<p class="leading-5 text-muted-foreground">
										This attachment is always read-only. It references the Downloader source and
										does not create a copy.
									</p>
								</div>
							{:else if intent === 'filesystem'}
								<div class="min-w-0 sm:col-span-2">
									<CustomComboBox
										bind:open={filesystemCombobox.open}
										label="ZFS filesystem"
										bind:value={filesystemCombobox.value}
										data={filesystemDatasetOptions}
										classes="space-y-1"
										placeholder="Choose a filesystem"
										width="w-[min(34rem,calc(100vw-3rem))]"
										multiple={false}
										disallowEmpty={true}
									></CustomComboBox>
								</div>
								<CustomValueInput
									label="Attachment label"
									placeholder="shared-data"
									bind:value={properties.name}
									classes="min-w-0 space-y-1"
								/>
								<CustomValueInput
									label="Guest-visible target"
									placeholder="shared_data"
									hint="Letters, numbers, periods, underscores, and hyphens only."
									bind:value={properties.filesystemTarget}
									classes="min-w-0 space-y-1"
								/>
								<CustomCheckbox
									label="Read-only share"
									bind:checked={properties.filesystemReadOnly}
									classes="flex items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2.5 sm:col-span-2"
								/>
							{:else if intent === 'advanced'}
								<CustomValueInput
									label="Name"
									placeholder="router-disk"
									bind:value={properties.name}
									classes="min-w-0 space-y-1 sm:col-span-2"
								/>
								<SimpleSelect
									label="Host source"
									placeholder="Select source"
									options={[
										{ value: 'raw', label: 'Regular RAW file' },
										{ value: 'zvol', label: 'Existing ZFS volume' }
									]}
									bind:value={properties.diskType}
									onChange={setBacking}
									classes={storageSelectClasses}
								/>
								<SimpleSelect
									label="Destination pool"
									placeholder="Select pool"
									options={pools.map((pool) => ({ value: pool.name, label: pool.name }))}
									bind:value={properties.pool}
									onChange={(value) => (properties.pool = value)}
									classes={storageSelectClasses}
								/>
								{#if properties.diskType === 'raw'}
									<CustomValueInput
										label="Absolute host path"
										placeholder="/var/tmp/router.img"
										hint="Sylve copies this file into a new managed RAW disk. The source remains."
										bind:value={properties.rawPath}
										classes="min-w-0 space-y-1 sm:col-span-2"
									/>
								{:else}
									<div class="min-w-0 sm:col-span-2">
										<CustomComboBox
											bind:open={zvolCombobox.open}
											label="ZFS volume"
											bind:value={zvolCombobox.value}
											data={availableZvolOptions}
											classes="space-y-1"
											placeholder="Choose an unattached volume"
											width="w-[min(34rem,calc(100vw-3rem))]"
											multiple={false}
											disallowEmpty={true}
										></CustomComboBox>
									</div>
								{/if}
								<SimpleSelect
									label="Emulation"
									placeholder="Select emulation"
									options={writableEmulationOptions}
									bind:value={properties.emulation}
									onChange={(value) => (properties.emulation = value as StorageEmulation)}
									classes={storageSelectClasses}
								/>
								<CustomValueInput
									label="Boot order"
									placeholder="0"
									type="number"
									bind:value={properties.bootOrder as number}
									classes="min-w-0 space-y-1"
								/>
							{/if}
						</div>
					</section>

					<OperationPreview
						{preview}
						icon={selectedIntent.icon}
						emulation={properties.emulation}
						rid={vm.rid}
					/>
				</div>

				<Dialog.Footer class="border-t pt-4">
					<Button
						size="sm"
						type="button"
						class="h-9 min-w-44"
						onclick={attach}
						disabled={properties.loading}
					>
						{#if properties.loading}
							<span class="icon-[eos-icons--loading] mr-2 size-4 animate-spin"></span>
							{selectedIntent.loading}
						{:else}
							{selectedIntent.action}
							<span class="icon-[lucide--arrow-right] ml-2 size-4"></span>
						{/if}
					</Button>
				</Dialog.Footer>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
