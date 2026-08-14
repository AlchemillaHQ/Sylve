<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { registerPlugin, type ProcessServerConfigFunction } from 'filepond';
	import FilePondPluginImageExifOrientation from 'filepond-plugin-image-exif-orientation';
	import FilePondPluginImagePreview from 'filepond-plugin-image-preview';
	import { storage } from '$lib';
	import { addFileOrFolder, revertFileExplorerUpload } from '$lib/api/system/file-explorer';
	import { isDemoMode } from '$lib/demo/runtime';
	import { reload } from '$lib/stores/api.svelte';
	import type { APIResponse } from '$lib/types/common';
	import {
		getFilePondRequestHeaders,
		parseFilePondUploadError,
		parseFilePondUploadID
	} from '$lib/utils/filepond';
	import FilePond from '../FilePond.svelte';
	interface Props {
		isOpen: boolean;
		onClose: () => void;
		hostname?: string;
		currentPath?: string;
		droppedFiles?: File[];
		onUploadComplete?: () => void;
	}

	let {
		isOpen = $bindable(false),
		onClose,
		hostname,
		currentPath = '/',
		droppedFiles = [],
		onUploadComplete
	}: Props = $props();

	registerPlugin(FilePondPluginImageExifOrientation, FilePondPluginImagePreview);

	let uploadReady = $derived(isDemoMode || Boolean(storage.token?.trim()));

	function getAPIError(result: APIResponse, fallback: string): string {
		if (Array.isArray(result.error)) return result.error.join(', ');
		return result.error || result.message || fallback;
	}

	function revertUpload(uniqueFileID: unknown, load: () => void, error: (message: string) => void) {
		const uploadId = typeof uniqueFileID === 'string' ? uniqueFileID.trim() : '';
		if (!uploadId) {
			load();
			return;
		}

		void revertFileExplorerUpload(uploadId, hostname).then((result) => {
			if (result.status === 'success') {
				load();
				return;
			}
			error(getAPIError(result, 'Failed to remove upload'));
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
		void addFileOrFolder(currentPath, file.name, false, hostname, file.size)
			.then((result) => {
				if (cancelled) return;
				if (result.status !== 'success') {
					error(getAPIError(result, 'Failed to upload file'));
					return;
				}
				progress(true, file.size, file.size);
				load(`demo-upload-${Date.now().toString(36)}`);
			})
			.catch(() => {
				if (!cancelled) error('Failed to upload file');
			});

		return {
			abort: () => {
				cancelled = true;
				abort();
			}
		};
	};

	let uploadServer = $derived.by(() => ({
		process: isDemoMode
			? processDemoUpload
			: {
					url: `/api/system/file-explorer/upload?path=${encodeURIComponent(currentPath)}`,
					method: 'POST' as const,
					headers: getFilePondRequestHeaders(hostname),
					onload: (response: string) => parseFilePondUploadID(response),
					onerror: (response: string) => parseFilePondUploadError(response)
				},
		revert: revertUpload
	}));

	function handleProcessFile(error: unknown, _file: unknown) {
		reload.auditLog = true;
		if (!error) onUploadComplete?.();
	}

	function handleRemoveFile() {
		onUploadComplete?.();
	}
</script>

<Dialog.Root bind:open={isOpen}>
	<Dialog.Content
		onInteractOutside={onClose}
		class="fixed flex transform flex-col gap-2 overflow-auto p-6 transition-all duration-300 ease-in-out lg:max-w-md"
		showCloseButton={true}
		{onClose}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[material-symbols--upload]"
					size="h-5 w-5"
					gap="gap-2"
					title="Upload Files"
				/>
			</Dialog.Title>
			<Dialog.Description>
				Upload files to <span class="font-mono">{currentPath}</span>. Files dropped on the explorer
				start automatically.
			</Dialog.Description>
		</Dialog.Header>
		<div class="app mt-4">
			{#if uploadReady}
				<FilePond
					name="filepond"
					server={uploadServer}
					files={droppedFiles}
					instantUpload={true}
					allowMultiple={true}
					maxParallelUploads={2}
					onprocessfile={handleProcessFile}
					onremovefile={handleRemoveFile}
					credits={false}
				/>
			{:else}
				<p class="text-destructive text-sm" role="alert">
					Upload authentication is unavailable. Sign in again before uploading files.
				</p>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
