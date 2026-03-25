<script lang="ts">
	import { storage } from '$lib';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { isValidAbsPath, sha256 } from '$lib/utils/string';
	import type { FilePond as FilePondType, FilePondErrorDescription, FilePondFile } from 'filepond';
	import { onMount } from 'svelte';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	type DownloadType = 'base-rootfs' | 'cloud-init' | 'uncategorized';

	interface UploadedFilePayload {
		path: string;
		downloadType: DownloadType;
		automaticExtraction: boolean;
		automaticRawConversion: boolean;
	}

	interface Props {
		open: boolean;
		stagingPath: string;
		onClose: () => void;
		onUploaded: (payload: UploadedFilePayload) => Promise<void> | void;
		loading?: boolean;
	}

	let {
		open = $bindable(false),
		stagingPath = '/tmp',
		onClose,
		onUploaded,
		loading = false
	}: Props = $props();

	const defaultOptions = {
		downloadType: 'uncategorized' as DownloadType,
		automaticExtraction: false,
		automaticRawConversion: false
	};

	let options = $state({ ...defaultOptions });
	let hash = $state('');
	let isProcessingUpload = $state(false);
	let pond: FilePondType;
	let name = 'filepond';

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
	});

	function resetState() {
		options = { ...defaultOptions };
		if (pond) {
			pond.removeFiles();
		}
	}

	function handleReset() {
		if (loading || isProcessingUpload) return;
		resetState();
	}

	function handleClose() {
		if (loading || isProcessingUpload) return;
		resetState();
		onClose();
	}

	function parseUploadResponse(response: string): string {
		try {
			const parsed = JSON.parse(response);
			const uploadedPath = parsed?.data?.path;
			if (typeof uploadedPath === 'string' && uploadedPath.length > 0) {
				return uploadedPath;
			}
		} catch (error) {
			console.error('Failed to parse upload response', error);
		}

		return response;
	}

	function parseUploadError(response: string): string {
		try {
			const parsed = JSON.parse(response);
			if (typeof parsed?.error === 'string') {
				return parsed.error;
			}
			if (typeof parsed?.message === 'string') {
				return parsed.message;
			}
		} catch (error) {
			console.error('Failed to parse upload error', error);
		}

		return response;
	}

	async function handleProcessFile(error: FilePondErrorDescription | null, file: FilePondFile) {
		if (error) {
			toast.error(error.main || 'Upload failed', { position: 'bottom-center' });
			return;
		}

		const uploadedPath = typeof file.serverId === 'string' ? file.serverId.trim() : '';
		if (!isValidAbsPath(uploadedPath)) {
			toast.error('Uploaded file path is invalid', { position: 'bottom-center' });
			return;
		}

		isProcessingUpload = true;
		try {
			await onUploaded({
				path: uploadedPath,
				downloadType: options.downloadType,
				automaticExtraction: options.automaticExtraction,
				automaticRawConversion: options.automaticRawConversion
			});
		} catch (uploadError) {
			console.error('Failed to start download from uploaded file', uploadError);
			toast.error('Failed to start download from uploaded file', { position: 'bottom-center' });
		} finally {
			isProcessingUpload = false;
		}
	}

	function handleDownloadTypeChange(value: string) {
		const selectedType = value as DownloadType;
		options.downloadType = selectedType;

		if (selectedType === 'base-rootfs' && options.automaticExtraction === false) {
			options.automaticExtraction = true;
		}
	}

	watch(
		() => open,
		(current) => {
			if (!current) {
				resetState();
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={handleClose}
		class="fixed flex max-h-[90vh] transform flex-col gap-2 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-md"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--upload] h-6 w-6"></span>
					Upload File
				</div>

				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" title="Reset" onclick={handleReset}>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button size="sm" variant="link" class="h-4" title="Close" onclick={handleClose}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="text-muted-foreground mt-2 text-xs">
			Upload destination:
			<span class="font-mono">{stagingPath}</span>
		</div>

		<div class="mt-2 flex flex-col gap-3">
			<SimpleSelect
				label="Download Type"
				placeholder="Select Download Type"
				options={[
					{ value: 'uncategorized', label: 'Uncategorized (ISOs, IMGs, etc.)' },
					{ value: 'base-rootfs', label: 'Base / RootFS' },
					{ value: 'cloud-init', label: 'Cloud-Init' }
				]}
				classes={{
					parent: 'flex-1 space-y-1 w-full',
					label: 'mb-2',
					trigger: 'w-full'
				}}
				bind:value={options.downloadType}
				onChange={handleDownloadTypeChange}
			/>

			<div class="flex flex-col gap-2">
				<CustomCheckbox
					label="Extract Automatically"
					bind:checked={options.automaticExtraction}
					classes="flex items-center gap-2"
				/>
				<CustomCheckbox
					label="Auto-convert to RAW"
					bind:checked={options.automaticRawConversion}
					classes="flex items-center gap-2"
				/>
			</div>
		</div>

		<div class="app mt-4">
			<FilePond
				bind:this={pond}
				{name}
				server={{
					process: {
						url:
							'/api/system/file-explorer/upload?path=' +
							encodeURIComponent(stagingPath) +
							'&hash=' +
							hash,
						method: 'POST',
						onload: parseUploadResponse,
						onerror: parseUploadError
					}
				}}
				allowMultiple={false}
				maxFiles={1}
				onprocessfile={handleProcessFile}
				credits={false}
			/>
		</div>
	</Dialog.Content>
</Dialog.Root>
