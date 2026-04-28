<script lang="ts">
	import { storage } from '$lib';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { isValidAbsPath, sha256 } from '$lib/utils/string';
	import type { FilePondErrorDescription, FilePondFile } from 'filepond';
	import { registerPlugin } from 'filepond';
	import FilePondPluginImageExifOrientation from 'filepond-plugin-image-exif-orientation';
	import FilePondPluginImagePreview from 'filepond-plugin-image-preview';
	import { onDestroy, onMount } from 'svelte';
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
	let uploadedPath = $state('');
	let pondRenderKey = $state(0);
	let closeResetTimer: ReturnType<typeof setTimeout> | undefined = undefined;
	let name = 'filepond';

	registerPlugin(FilePondPluginImageExifOrientation, FilePondPluginImagePreview);

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
	});

	function resetState() {
		options = { ...defaultOptions };
		uploadedPath = '';
		pondRenderKey += 1;
	}

	function resetOptionsOnly() {
		options = { ...defaultOptions };
		uploadedPath = '';
	}

	function handleReset() {
		if (loading || isProcessingUpload) return;
		resetState();
	}

	function handleClose() {
		if (loading || isProcessingUpload) return;
		onClose();
	}

	watch(
		() => open,
		(current) => {
			if (current) {
				if (closeResetTimer) {
					clearTimeout(closeResetTimer);
					closeResetTimer = undefined;
				}
				return;
			}

			resetOptionsOnly();
			closeResetTimer = setTimeout(() => {
				pondRenderKey += 1;
				closeResetTimer = undefined;
			}, 320);
		}
	);

	onDestroy(() => {
		if (closeResetTimer) {
			clearTimeout(closeResetTimer);
			closeResetTimer = undefined;
		}
	});

	function getUploadedPath(file: FilePondFile): string {
		const serverId = typeof file.serverId === 'string' ? file.serverId.trim() : '';
		if (isValidAbsPath(serverId)) {
			return serverId;
		}

		try {
			const parsed = JSON.parse(serverId);
			const uploadedPath = parsed?.data?.path;
			if (typeof uploadedPath === 'string' && isValidAbsPath(uploadedPath)) {
				return uploadedPath;
			}
		} catch {
			// serverId can be a non-JSON id depending on backend response shape
		}

		return '';
	}

	async function handleProcessFile(error: FilePondErrorDescription | null, file: FilePondFile) {
		if (error) {
			toast.error(error.body || 'Upload failed', { position: 'bottom-center' });
			return;
		}

		const resolvedPath = getUploadedPath(file);
		if (!resolvedPath) {
			toast.error('Uploaded file path is invalid', { position: 'bottom-center' });
			return;
		}

		uploadedPath = resolvedPath;
		toast.success('File uploaded, you can complete upload now', { position: 'bottom-center' });
	}

	function handleDownloadTypeChange(value: string) {
		const selectedType = value as DownloadType;
		options.downloadType = selectedType;

		if (selectedType === 'base-rootfs' && options.automaticExtraction === false) {
			options.automaticExtraction = true;
		}
	}

	async function handleStartDownload() {
		if (loading || isProcessingUpload) return;
		if (!isValidAbsPath(uploadedPath)) {
			toast.error('Upload a file first', { position: 'bottom-center' });
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
			console.error('Failed to start move from uploaded file', uploadError);
			toast.error('Failed to start move from uploaded file', { position: 'bottom-center' });
		} finally {
			isProcessingUpload = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={handleClose}
		class="fixed flex max-h-[90vh] transform flex-col gap-2 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-md"
		showCloseButton={true}
		showResetButton={true}
		onClose={handleClose}
		onReset={handleReset}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon icon="icon-[material-symbols--upload]" size="h-6 w-6" gap="gap-2" title="Upload File" />
			</Dialog.Title>
		</Dialog.Header>

		<div class="text-muted-foreground mt-2 text-xs hidden">
			<span class="font-mono">{stagingPath}</span>
		</div>

		{#key pondRenderKey}
			<FilePond
				class="min-h-18! overflow-hidden! mb-1!"
				{name}
				server={'/api/system/file-explorer/upload?path=' +
					encodeURIComponent(stagingPath) +
					'&hash=' +
					hash}
				allowMultiple={false}
				maxFiles={1}
				allowRevert={false}
				stylePanelLayout="compact"
				onprocessfile={handleProcessFile}
				credits={false}
			/>
		{/key}

		<div class="mt-0 flex flex-col gap-3">
			<SimpleSelect
				label="Upload Type"
				placeholder="Select Upload Type"
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

			<div class="flex flex-row gap-2">
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

		<Dialog.Footer class="mt-2 flex justify-end">
			<Button
				size="sm"
				onclick={handleStartDownload}
				disabled={!uploadedPath || loading || isProcessingUpload}
			>
				{#if loading || isProcessingUpload}
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
				{:else}
					<span>Complete Upload</span>
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
