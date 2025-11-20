<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { store } from '$lib/stores/auth';
	import { sha256 } from '$lib/utils/string';
	import type { FilePond as FilePondType } from 'filepond';
	import { registerPlugin } from 'filepond';
	import FilePondPluginImageExifOrientation from 'filepond-plugin-image-exif-orientation';
	import FilePondPluginImagePreview from 'filepond-plugin-image-preview';
	import { onMount } from 'svelte';
	import FilePond from '../FilePond.svelte';
	interface Props {
		isOpen: boolean;
		onClose: () => void;
		currentPath?: string;
		droppedFiles?: File[];
		onUploadComplete?: () => void;
	}

	let {
		isOpen = $bindable(false),
		onClose,
		currentPath = '/',
		droppedFiles = [],
		onUploadComplete
	}: Props = $props();

	registerPlugin(FilePondPluginImageExifOrientation, FilePondPluginImagePreview);

	let pond: FilePondType;

	let name = 'filepond';
	let hash = $state('');

	onMount(async () => {
		hash = await sha256($store, 1);
	});

	function handleInit() {
		if (pond && droppedFiles.length > 0) {
			droppedFiles.forEach((file) => {
				pond.addFile(file);
			});
		}
	}

	function handleAddFile(err: any, fileItem: any) {
		console.log('A file has been added', fileItem);
	}

	function handleProcessFile(error: any, file: any) {
		if (error) {
			console.error('Upload failed:', error);
			return;
		}
		if (onUploadComplete) {
			onUploadComplete();
		}
	}

	function handleRemoveFile() {
		if (onUploadComplete) {
			onUploadComplete();
		}
	}

	$effect(() => {
		if (pond && droppedFiles.length > 0 && isOpen) {
			pond.removeFiles();
			droppedFiles.forEach((file) => {
				pond.addFile(file);
			});
		}
	});
</script>

<Dialog.Root bind:open={isOpen}>
	<Dialog.Content
		onInteractOutside={onClose}
		class="fixed flex transform flex-col gap-2 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-md"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--upload] h-6 w-6"></span>
					Upload File
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" title="Close" onclick={onClose}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>
		<div class="app mt-4">
			<FilePond
				bind:this={pond}
				{name}
				server={'/api/system/file-explorer/upload?path=' +
					encodeURIComponent(currentPath) +
					'&hash=' +
					hash}
				allowMultiple={true}
				oninit={handleInit}
				onaddfile={handleAddFile}
				onprocessfile={handleProcessFile}
				onremovefile={handleRemoveFile}
				credits={false}
			/>
		</div>
	</Dialog.Content>
</Dialog.Root>
