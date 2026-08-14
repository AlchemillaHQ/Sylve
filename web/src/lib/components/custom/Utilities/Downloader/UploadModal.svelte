<script lang="ts">
	import { storage } from '$lib';
	import { abortDownloaderUpload, completeDownloaderUpload } from '$lib/api/utilities/downloader';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import { isDemoMode } from '$lib/demo/runtime';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { getDownloaderProcessingOptionsError } from '$lib/utils/downloader-processing';
	import {
		getFilePondRequestHeaders,
		getFilePondRequestHostname,
		parseFilePondUploadError,
		parseFilePondUploadID
	} from '$lib/utils/filepond';
	import { handleAPIError } from '$lib/utils/http';
	import type {
		FilePond as FilePondType,
		FilePondErrorDescription,
		FilePondFile,
		ProcessServerConfigFunction
	} from 'filepond';
	import { registerPlugin } from 'filepond';
	import FilePondPluginImageExifOrientation from 'filepond-plugin-image-exif-orientation';
	import FilePondPluginImagePreview from 'filepond-plugin-image-preview';
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';

	type DownloadType = 'base-rootfs' | 'cloud-init' | 'uncategorized';
	type CompletionState = 'pending' | 'completing' | 'done' | 'failed';

	interface DownloaderOptions {
		downloadType: DownloadType;
		automaticExtraction: boolean;
		automaticRawConversion: boolean;
	}

	interface StagedItem {
		pondId: string;
		uploadId: string;
		hostname: string;
		name: string;
		size: number;
		completionState: CompletionState;
		completionError: string;
		options: DownloaderOptions;
	}

	interface Props {
		open: boolean;
		onClose: () => void;
		onCompleted?: () => void;
	}

	let { open = $bindable(false), onClose, onCompleted }: Props = $props();

	const clientUploadConcurrency = 2;
	const downloadTypeOptions = [
		{ value: 'uncategorized', label: 'Uncategorized (ISOs, IMGs, etc.)' },
		{ value: 'base-rootfs', label: 'Base / RootFS' },
		{ value: 'cloud-init', label: 'Cloud-Init' }
	];
	const defaultOptions: DownloaderOptions = {
		downloadType: 'uncategorized',
		automaticExtraction: false,
		automaticRawConversion: false
	};

	let sharedOptions = $state<DownloaderOptions>({ ...defaultOptions });
	let stagedItems = $state<StagedItem[]>([]);
	let pond = $state<FilePondType | undefined>(undefined);
	let isCompletingAll = $state(false);
	let uploadHostname = $state(getFilePondRequestHostname());
	let uploadReady = $derived(isDemoMode || Boolean(storage.token?.trim()));

	let completableItems = $derived(
		stagedItems.filter(
			(item) =>
				item.completionState !== 'done' &&
				item.completionState !== 'completing' &&
				!processingOptionsError(item)
		)
	);
	let unfinishedItems = $derived(stagedItems.filter((item) => item.completionState !== 'done'));
	let hasCompletedItems = $derived(stagedItems.some((item) => item.completionState === 'done'));

	registerPlugin(FilePondPluginImageExifOrientation, FilePondPluginImagePreview);

	watch(
		() => open,
		(current, previous) => {
			if (current && previous !== true) {
				uploadHostname = getFilePondRequestHostname();
			}
			if (previous === true && !current) {
				clearQueue();
			}
		}
	);

	onDestroy(clearQueue);

	function getAPIError(result: APIResponse, fallback: string): string {
		if (Array.isArray(result.error)) return result.error.join(', ');
		return result.error || result.message || fallback;
	}

	function getUnknownError(error: unknown, fallback: string): string {
		if (error instanceof Error && error.message) return error.message;
		if (typeof error === 'string' && error) return error;
		return fallback;
	}

	function findItemByPondID(pondId: string): StagedItem | undefined {
		return stagedItems.find((item) => item.pondId === pondId);
	}

	function handleProcessFile(error: FilePondErrorDescription | null, file: FilePondFile) {
		if (error) return;

		const uploadId = typeof file.serverId === 'string' ? file.serverId.trim() : '';
		if (!uploadId) {
			toast.error(`${file.filename}: upload receipt is invalid`, {
				position: 'bottom-center'
			});
			return;
		}

		const existing = findItemByPondID(file.id);
		if (existing) {
			existing.uploadId = uploadId;
			existing.hostname = uploadHostname;
			existing.completionState = 'pending';
			existing.completionError = '';
			return;
		}

		stagedItems.push({
			pondId: file.id,
			uploadId,
			hostname: uploadHostname,
			name: file.filename,
			size: file.fileSize,
			completionState: 'pending',
			completionError: '',
			options: { ...sharedOptions }
		});
	}

	function handleRemoveFile(error: FilePondErrorDescription | null, file: FilePondFile) {
		const item = findItemByPondID(file.id);
		if (!item) return;
		if (error) {
			item.completionError = error.body || 'Failed to remove upload';
			return;
		}

		const index = stagedItems.findIndex((candidate) => candidate.pondId === file.id);
		if (index !== -1) stagedItems.splice(index, 1);
	}

	function revertUpload(uniqueFileID: unknown, load: () => void, error: (message: string) => void) {
		const uploadId = typeof uniqueFileID === 'string' ? uniqueFileID.trim() : '';
		if (!uploadId) {
			load();
			return;
		}

		const item = stagedItems.find((candidate) => candidate.uploadId === uploadId);
		void abortDownloaderUpload(uploadId, item?.hostname || uploadHostname).then((result) => {
			if ('uploadId' in result) {
				if (result.status === 'completed' && item) {
					item.completionState = 'done';
					item.completionError = '';
					onCompleted?.();
				}
				load();
				return;
			}

			const response = result as APIResponse;
			const responseError = getAPIError(response, 'Failed to abort upload');
			if (item) item.completionError = responseError;
			error(responseError);
		});
	}

	const processDemoUpload: ProcessServerConfigFunction = (
		_fieldName,
		file,
		_metadata,
		load,
		error,
		progress,
		abort
	) => {
		let cancelled = false;
		const timeout = window.setTimeout(async () => {
			if (cancelled) return;
			try {
				const { stageDemoDownloaderUpload } = await import('$lib/demo/admin-fixtures');
				if (cancelled) return;
				progress(true, file.size, file.size);
				load(stageDemoDownloaderUpload(uploadHostname, file.name, file.size));
			} catch {
				if (!cancelled) error('Failed to upload file');
			}
		}, 350);

		return {
			abort: () => {
				cancelled = true;
				window.clearTimeout(timeout);
				abort();
			}
		};
	};

	let uploadServer = $derived.by(() => ({
		process: isDemoMode
			? processDemoUpload
			: {
					url: '/api/utilities/downloader-uploads',
					method: 'POST' as const,
					headers: getFilePondRequestHeaders(uploadHostname),
					onload: (response: string) => parseFilePondUploadID(response),
					onerror: (response: string) => parseFilePondUploadError(response)
				},
		revert: revertUpload
	}));

	function handleSharedDownloadTypeChange(value: string) {
		sharedOptions.downloadType = value as DownloadType;
		if (sharedOptions.downloadType === 'base-rootfs') {
			sharedOptions.automaticExtraction = true;
		}
	}

	function handleItemDownloadTypeChange(item: StagedItem, value: string) {
		item.options.downloadType = value as DownloadType;
		if (item.options.downloadType === 'base-rootfs') {
			item.options.automaticExtraction = true;
		}
	}

	function applySharedOptions() {
		let updated = 0;
		for (const item of stagedItems) {
			if (item.completionState === 'done' || item.completionState === 'completing') continue;
			item.options = { ...sharedOptions };
			updated += 1;
		}

		if (updated > 0) {
			toast.success(`Applied defaults to ${updated} file${updated === 1 ? '' : 's'}`, {
				position: 'bottom-center'
			});
		}
	}

	async function completeItem(item: StagedItem, notify = true): Promise<boolean> {
		if (item.completionState === 'done' || item.completionState === 'completing') {
			return false;
		}
		const optionsError = processingOptionsError(item);
		if (optionsError) {
			item.completionState = 'failed';
			item.completionError = optionsError;
			return false;
		}

		item.completionState = 'completing';
		item.completionError = '';
		try {
			const result = await completeDownloaderUpload(
				item.uploadId,
				item.options.downloadType,
				item.options.automaticExtraction,
				item.options.automaticRawConversion,
				item.hostname
			);

			if ('downloadId' in result) {
				item.completionState = 'done';
				onCompleted?.();
				if (notify) {
					toast.success(`${item.name} added to the downloader`, {
						position: 'bottom-center'
					});
				}
				return true;
			}

			const response = result as APIResponse;
			item.completionState = 'failed';
			item.completionError = getAPIError(response, 'Failed to complete upload');
			handleAPIError(response);
		} catch (error) {
			item.completionState = 'failed';
			item.completionError = getUnknownError(error, 'Failed to complete upload');
		}

		if (notify) {
			toast.error(`${item.name}: ${item.completionError}`, {
				position: 'bottom-center'
			});
		}
		return false;
	}

	async function completeAll() {
		const items = [...completableItems];
		if (items.length === 0 || isCompletingAll) return;

		isCompletingAll = true;
		try {
			const results = await Promise.allSettled(items.map((item) => completeItem(item, false)));
			const completed = results.filter(
				(result) => result.status === 'fulfilled' && result.value
			).length;
			const failed = results.length - completed;
			if (completed > 0) {
				toast.success(`Added ${completed} file${completed === 1 ? '' : 's'} to the downloader`, {
					position: 'bottom-center'
				});
			}
			if (failed > 0) {
				toast.error(`${failed} file${failed === 1 ? '' : 's'} could not be completed`, {
					position: 'bottom-center'
				});
			}
		} finally {
			isCompletingAll = false;
		}
	}

	function cancelUnfinished() {
		for (const file of pond?.getFiles() ?? []) {
			const item = findItemByPondID(file.id);
			if (item?.completionState === 'done') continue;
			pond?.removeFile(file.id, { revert: true });
		}
	}

	function clearQueue() {
		for (const file of pond?.getFiles() ?? []) {
			const item = findItemByPondID(file.id);
			pond?.removeFile(file.id, { revert: item?.completionState !== 'done' });
		}
		stagedItems.splice(0, stagedItems.length);
	}

	function handleReset() {
		sharedOptions = { ...defaultOptions };
		clearQueue();
	}

	function handleClose() {
		onClose();
	}

	function statusLabel(item: StagedItem): string {
		if (item.completionState === 'done') return 'Done';
		if (item.completionState === 'completing') return 'Completing';
		if (item.completionState === 'failed') return 'Completion failed';
		return 'Ready to complete';
	}

	function statusClass(item: StagedItem): string {
		if (item.completionState === 'done') return 'text-emerald-600 dark:text-emerald-400';
		if (item.completionState === 'failed') return 'text-destructive';
		return 'text-sky-600 dark:text-sky-400';
	}

	function canEditOptions(item: StagedItem): boolean {
		return item.completionState !== 'done' && item.completionState !== 'completing';
	}

	function processingOptionsError(item: StagedItem): string {
		return getDownloaderProcessingOptionsError(
			item.name,
			item.options.automaticExtraction,
			item.options.automaticRawConversion
		);
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={handleClose}
		class="fixed flex max-h-[90vh] transform flex-col gap-3 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-3xl"
		showCloseButton={true}
		showResetButton={true}
		onClose={handleClose}
		onReset={handleReset}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[material-symbols--upload]"
					size="h-6 w-6"
					gap="gap-2"
					title="Upload Files"
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if uploadReady}
			<FilePond
				bind:instance={pond}
				class="min-h-18! mb-1!"
				name="filepond"
				server={uploadServer}
				allowMultiple={true}
				maxParallelUploads={clientUploadConcurrency}
				allowRevert={true}
				onprocessfile={handleProcessFile}
				onremovefile={handleRemoveFile}
				credits={false}
			/>
		{:else}
			<p class="text-destructive text-sm" role="alert">
				Upload authentication is unavailable. Sign in again before uploading files.
			</p>
		{/if}

		<div class="rounded-md border p-3">
			<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
				<div>
					<p class="text-sm font-medium">Batch defaults</p>
					<p class="text-muted-foreground text-xs">
						Completed transfers inherit these values and can still be adjusted per file.
					</p>
				</div>
				<Button
					size="sm"
					variant="outline"
					onclick={applySharedOptions}
					disabled={stagedItems.length === 0 || isCompletingAll}
				>
					Apply to unfinished
				</Button>
			</div>

			<div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-end">
				<SimpleSelect
					label="Upload Type"
					placeholder="Select Upload Type"
					options={downloadTypeOptions}
					classes={{
						parent: 'space-y-1 w-full',
						label: 'mb-2',
						trigger: 'w-full'
					}}
					bind:value={sharedOptions.downloadType}
					onChange={handleSharedDownloadTypeChange}
				/>

				<div class="flex flex-wrap gap-3 pb-1">
					<CustomCheckbox
						label="Extract Automatically"
						bind:checked={sharedOptions.automaticExtraction}
						classes="flex items-center gap-2"
					/>
					<CustomCheckbox
						label="Auto-convert to RAW"
						bind:checked={sharedOptions.automaticRawConversion}
						classes="flex items-center gap-2"
					/>
				</div>
			</div>
		</div>

		{#if stagedItems.length > 0}
			<div class="flex flex-col gap-3" aria-label="Staged uploads">
				{#each stagedItems as item (item.pondId)}
					<div class="rounded-md border p-3">
						<div class="flex min-w-0 flex-wrap items-start justify-between gap-2">
							<div class="min-w-0">
								<p class="truncate text-sm font-medium" title={item.name}>{item.name}</p>
								<p class="text-muted-foreground text-xs">{formatBytesBinary(item.size)}</p>
							</div>
							<span class={`shrink-0 text-xs font-medium ${statusClass(item)}`}>
								{statusLabel(item)}
							</span>
						</div>

						{#if item.completionError}
							<p class="text-destructive mt-2 text-xs" role="alert">
								{item.completionError}
							</p>
						{/if}
						{#if processingOptionsError(item)}
							<p class="text-destructive mt-2 text-xs" role="alert">
								{processingOptionsError(item)}
							</p>
						{/if}

						<div class="mt-3 grid gap-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-end">
							<SimpleSelect
								label="Upload Type"
								placeholder="Select Upload Type"
								options={downloadTypeOptions}
								classes={{
									parent: 'space-y-1 w-full',
									label: 'mb-1',
									trigger: 'w-full'
								}}
								bind:value={item.options.downloadType}
								onChange={(value) => handleItemDownloadTypeChange(item, value)}
								disabled={!canEditOptions(item)}
							/>

							<div class="flex flex-wrap gap-3 pb-1">
								<CustomCheckbox
									label="Extract"
									bind:checked={item.options.automaticExtraction}
									classes="flex items-center gap-2"
									disabled={!canEditOptions(item)}
								/>
								<CustomCheckbox
									label="Convert to RAW"
									bind:checked={item.options.automaticRawConversion}
									classes="flex items-center gap-2"
									disabled={!canEditOptions(item)}
								/>
							</div>
						</div>

						{#if item.completionState !== 'done'}
							<div class="mt-3 flex justify-end">
								<Button
									size="sm"
									onclick={() => completeItem(item)}
									disabled={item.completionState === 'completing' ||
										Boolean(processingOptionsError(item))}
								>
									{#if item.completionState === 'completing'}
										<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
										Completing
									{:else if item.completionState === 'failed'}
										Retry completion
									{:else}
										Complete
									{/if}
								</Button>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}

		<Dialog.Footer class="mt-1 flex flex-wrap justify-end gap-2">
			{#if unfinishedItems.length > 0}
				<Button size="sm" variant="outline" onclick={cancelUnfinished} disabled={isCompletingAll}>
					{#if hasCompletedItems}
						Cancel Remaining
					{:else}
						Cancel All
					{/if}
				</Button>
			{/if}
			<Button
				size="sm"
				onclick={completeAll}
				disabled={completableItems.length === 0 || isCompletingAll}
			>
				{#if isCompletingAll}
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					Completing
				{:else}
					Complete All ({completableItems.length})
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
