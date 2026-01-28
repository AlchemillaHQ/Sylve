<script lang="ts">
	import {
		bulkDeleteDownloads,
		deleteDownload,
		getDownloads,
		getSignedURL,
		startDownload
	} from '$lib/api/utilities/downloader';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { type APIResponse } from '$lib/types/common';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import {
		addTrackersToMagnet,
		isDownloadURL,
		isValidAbsPath,
		isValidFileName
	} from '$lib/utils/string';
	import { generateTableData } from '$lib/utils/utilities/downloader';
	import { toast } from 'svelte-sonner';
	import isMagnet from 'validator/lib/isMagnetURI';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { sleep } from '$lib/utils';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import { untrack } from 'svelte';
    import { watch } from 'runed';

	interface Data {
		downloads: Download[];
	}

	let { data }: { data: Data } = $props();
	let reload = $state(false);

	const visible = new IsDocumentVisible();
	const downloads = resource(
		() => 'downloads',
		async () => {
			const results = await getDownloads();
			updateCache('downloads', results);
			return results;
		},
		{
			initialValue: data.downloads
		}
	);

	$effect(() => {
		if (visible.current) {
			untrack(() => {
				downloads.refetch();
			});
		}
	});

	$effect(() => {
		if (reload) {
			downloads.refetch();
			reload = false;
		}
	});

	useInterval(1000, {
		callback: () => {
			const incomplete = (downloads.current as Download[]).some(
				(d) => d.status !== 'done' && d.status !== 'failed'
			);

			if (incomplete) {
				downloads.refetch();
			}
		}
	});

	let options = {
		isOpen: false,
		isDelete: false,
		isBulkDelete: false,
		title: '',
		url: '',
		name: '',
		ignoreTLS: false,
		automaticExtraction: false,
		automaticRawConversion: false,
		loading: false,
		downloadType: 'uncategorized' as 'base-rootfs' | 'uncategorized'
	};

	let modalState = $state(options);
	let tableData = $derived(generateTableData(downloads.current as Download[]));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);
	let onlyParentsSelected: boolean = $derived.by(() => {
		if (activeRows) {
			for (const row of activeRows) {
				if (row.type === '-') {
					return false;
				}
			}
		}

		return true;
	});

	let onlyChildSelected: boolean = $derived.by(() => {
		let hasParent = false;
		if (activeRows) {
			for (const row of activeRows) {
				if (row.type !== '-') {
					hasParent = true;
					break;
				}
			}
		}
		return !hasParent;
	});

	let httpDownloadSelected: boolean = $derived.by(() => {
		if (activeRows && activeRows.length === 1) {
			const row = activeRows[0];
			return row.type === 'http';
		}
		return false;
	});

	let isDownloadCompleted: boolean = $derived.by(() => {
		if (activeRows && activeRows.length === 1) {
			const row = activeRows[0];
			if (row.progress === '-') {
				const parent = downloads.current.find((d) => d.uuid === row.parentUUID);
				return parent ? parent.progress === 100 : false;
			} else if (row.progress === 100) {
				return true;
			}
		}
		return false;
	});

	async function newDownload() {
		if (!modalState.url) {
			toast.error('Please enter a valid URL', { position: 'bottom-center' });
			return;
		}

		if (
			!isMagnet(modalState.url) &&
			!isDownloadURL(modalState.url) &&
			!isValidAbsPath(modalState.url)
		) {
			toast.error('Please enter a valid Magnet, HTTP URL or Path', { position: 'bottom-center' });
			return;
		}

		if (isMagnet(modalState.url)) {
			modalState.url = addTrackersToMagnet(modalState.url);
		}

		if (modalState.name && !isValidFileName(modalState.name)) {
			toast.error('Invalid file name', { position: 'bottom-center' });
			return;
		}

		if (!modalState.downloadType) {
			modalState.downloadType = 'uncategorized';
		}

		modalState.loading = true;

		await sleep(500);

		const result = await startDownload(
			modalState.url,
			modalState.downloadType,
			modalState.name || undefined,
			modalState.ignoreTLS,
			modalState.automaticExtraction,
			modalState.automaticRawConversion
		);

		if (result) {
			modalState = options;
			reload = true;
			toast.success('Download started', { position: 'bottom-center' });
		} else {
			toast.error('Download failed', { position: 'bottom-center' });
		}
	}

	async function handleDelete() {
		if (activeRows && activeRows.length == 1) {
			modalState.isDelete = true;
			modalState.title = activeRows[0].name;
		}

		if (activeRows && activeRows.length > 1) {
			for (const row of activeRows) {
				if (row.type !== 'http' && row.type !== 'torrent') {
					modalState.isBulkDelete = false;
					modalState.title = '';
					return;
				}
			}
			modalState.isBulkDelete = true;
			modalState.title = `${activeRows.length} downloads`;
		}
	}

	async function handleDownload() {
		const row = activeRows ? activeRows[0] : null;
		if (row) {
			const result = await getSignedURL(row.name as string, (row.parentUUID as string) || row.uuid);
			if (isAPIResponse(result) && result.status === 'success') {
				const url = result.data as string;
				const link = document.createElement('a');
				link.href = url;
				link.download = row.name as string;
				document.body.appendChild(link);
				link.click();
			} else {
				handleAPIError(result as APIResponse);
				toast.error('Failed to get download link', { position: 'bottom-center' });
			}
		}
	}

	async function handleCopyURL() {
		const row = activeRows ? activeRows[0] : null;
		if (row) {
			const result = await getSignedURL(row.name as string, (row.parentUUID as string) || row.uuid);
			if (isAPIResponse(result) && result.status === 'success') {
				const url = result.data as string;
				const fullURl = new URL(url, window.location.origin).toString();
				await navigator.clipboard.writeText(fullURl);
				toast.success('Download URL copied to clipboard', { position: 'bottom-center' });
			} else {
				handleAPIError(result as APIResponse);
				toast.error('Failed to get download link', { position: 'bottom-center' });
			}
		}
	}

    watch(() => modalState.downloadType, () => {
        if (modalState.downloadType === 'base-rootfs') {
            if (modalState.automaticExtraction === false) {
                modalState.automaticExtraction = true;
            }
        }
    })
</script>

{#snippet button(type: string)}
	{#if type === 'download' && onlyChildSelected && isDownloadCompleted}
		{#if activeRows && activeRows.length == 1}
			<Button onclick={handleDownload} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--download] mr-1 h-4 w-4"></span>
					<span>Download</span>
				</div>
			</Button>
		{/if}
	{/if}

	{#if type === 'download' && httpDownloadSelected && isDownloadCompleted}
		{#if activeRows && activeRows.length == 1}
			<Button onclick={handleDownload} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--download] mr-1 h-4 w-4"></span>
					<span>Download</span>
				</div>
			</Button>
		{/if}
	{/if}

	{#if type === 'copy' && ((httpDownloadSelected && isDownloadCompleted) || (onlyChildSelected && isDownloadCompleted))}
		{#if activeRows && activeRows.length == 1}
			<Button onclick={handleCopyURL} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--content-copy] mr-1 h-4 w-4"></span>
					<span>Copy URL</span>
				</div>
			</Button>
		{/if}
	{/if}

	{#if type === 'delete' && onlyParentsSelected}
		{#if activeRows && activeRows.length >= 1}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

					<span>{activeRows.length > 1 ? 'Bulk Delete' : 'Delete'}</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modalState.isOpen = true)} size="sm" class="h-6">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('download')}
		{@render button('copy')}
		{@render button('delete')}
	</div>

	<Dialog.Root bind:open={modalState.isOpen}>
		<Dialog.Content class="gap-0 p-3 max-w-xl">
			<div class="flex items-center justify-between py-1 pb-2">
				<Dialog.Header class="flex-1">
					<Dialog.Title>
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--download] text-primary h-5 w-5"></span>
							<span>Download</span>
						</div>
					</Dialog.Title>
				</Dialog.Header>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="ghost"
						class="h-8"
						title={'Reset'}
						onclick={() => {
							modalState.isOpen = true;
							modalState.url = '';
						}}
					>
						<span class="icon-[radix-icons--reset] h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="ghost"
						class="h-8"
						title={'Close'}
						onclick={() => {
							modalState.isOpen = false;
							modalState.url = '';
						}}
					>
						<span class="icon-[material-symbols--close-rounded] h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</div>

			<CustomValueInput
				label={'Magnet / HTTP URL / Path'}
				placeholder="magnet:?xt=urn:btih:7d5210a711291d7181d6e074ce5ebd56f3fedd60"
				bind:value={modalState.url}
				classes="flex-1 space-y-1"
				type="textarea"
				textAreaClasses="h-24 w-full break-all"
			/>

			{#if (modalState.url && isDownloadURL(modalState.url)) || isValidAbsPath(modalState.url)}
				<div class="flex flex-col gap-4">
					<div class="flex flex-row gap-4">
						<CustomValueInput
							label={'Optional File Name'}
							placeholder="freebsd-14.3-base-amd64.txz"
							bind:value={modalState.name}
							classes="flex-1 space-y-1 mt-2"
						/>

						<SimpleSelect
							label="Download Type"
							placeholder="Select Download Type"
							options={[
								{ value: 'uncategorized', label: 'Uncategorized (ISOs, IMGs, etc.)' },
								{ value: 'base-rootfs', label: 'Base / RootFS' },
								{ value: 'cloud-init', label: 'Cloud-Init' }
							]}
							classes={{
								parent: 'mt-2.5 flex-1 space-y-1 w-full',
								label: 'mb-2',
								trigger: 'w-full'
							}}
							bind:value={modalState.downloadType}
							onChange={(value) =>
								(modalState.downloadType = value as 'base-rootfs' | 'uncategorized')}
						/>
					</div>

					<div class="mt-2 flex flex-row gap-2">
						{#if isDownloadURL(modalState.url)}
							<CustomCheckbox
								label="Ignore TLS Errors"
								bind:checked={modalState.ignoreTLS}
								classes="flex items-center gap-2"
							/>
						{/if}
						<CustomCheckbox
							label="Extract Automatically"
							bind:checked={modalState.automaticExtraction}
							classes="flex items-center gap-2"
						/>

						<CustomCheckbox
							label="Auto-convert to RAW"
							bind:checked={modalState.automaticRawConversion}
							classes="flex items-center gap-2"
						/>
					</div>
				</div>
			{/if}

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2 py-2">
					<Button onclick={newDownload} type="submit" size="sm">
						{#if modalState.loading}
							<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						{:else}
							<span>Download</span>
						{/if}
					</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<TreeTable
		data={tableData}
		name="tt-downloader"
		multipleSelect={true}
		bind:parentActiveRow={activeRows}
		bind:query
	/>

	<AlertDialog
		open={modalState.isDelete}
		names={{ parent: 'download', element: modalState?.title || '' }}
		actions={{
			onConfirm: async () => {
				const id = activeRows ? activeRows[0]?.id : null;
				const result = await deleteDownload(id as number);
				reload = true;
				if (isAPIResponse(result) && result.status === 'success') {
					modalState = options;
					activeRows = null;
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete download', { position: 'bottom-center' });
				}
			},
			onCancel: () => {
				modalState = options;
				modalState.title = '';
			}
		}}
	></AlertDialog>

	<AlertDialog
		open={modalState.isBulkDelete}
		names={{ parent: 'download', element: modalState?.title || '' }}
		actions={{
			onConfirm: async () => {
				const ids = activeRows ? activeRows.map((row) => row.id) : [];
				const result = await bulkDeleteDownloads(ids as number[]);
				reload = true;
				if (isAPIResponse(result) && result.status === 'success') {
					modalState = options;
					activeRows = null;
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete downloads', { position: 'bottom-center' });
				}
			},
			onCancel: () => {
				modalState = options;
				modalState.title = '';
			}
		}}
	></AlertDialog>
</div>
