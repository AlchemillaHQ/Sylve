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
	import { useQueries } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';
	import isMagnet from 'validator/lib/isMagnetURI';

	interface Data {
		downloads: Download[];
	}

	let { data }: { data: Data } = $props();
	const results = useQueries([
		{
			queryKey: ['downloads'],
			queryFn: async () => {
				return await getDownloads();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.downloads,
			onSuccess: (data: Download[]) => {
				updateCache('downloads', data);
			}
		}
	]);

	let modalState = $state({
		isOpen: false,
		isDelete: false,
		isBulkDelete: false,
		title: '',
		url: '',
		name: '',
		ignoreTLS: false
	});

	let downloads = $derived($results[0].data as Download[]);
	let tableData = $derived(generateTableData(downloads));
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
				const parent = downloads.find((d) => d.uuid === row.parentUUID);
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

		const result = await startDownload(
			modalState.url,
			modalState.name || undefined,
			modalState.ignoreTLS
		);
		if (result) {
			modalState.isOpen = false;
			modalState.url = '';
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
</script>

{#snippet button(type: string)}
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
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modalState.isOpen = true)} size="sm" class="h-6  ">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('delete')}
		{@render button('download')}
	</div>

	<Dialog.Root bind:open={modalState.isOpen}>
		<Dialog.Content class="w-[80%] gap-0 overflow-hidden p-3 lg:max-w-xl">
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
				placeholder="magnet:?xt=urn:btih:7d5210a711291d7181d6e074ce5ebd56f3fedd60&dn=debian-12.10.0-amd64-netinst.iso&xl=663748608&tr=http%3A%2F%2Fbttracker.debian.org%3A6969%2Fannounce"
				bind:value={modalState.url}
				classes="flex-1 space-y-1"
			/>

			{#if modalState.url && isDownloadURL(modalState.url)}
				<div class="flex flex-col gap-4">
					<CustomValueInput
						label={'Optional File Name'}
						placeholder="freebsd-14.3-base-amd64.txz"
						bind:value={modalState.name}
						classes="flex-1 space-y-1 mt-2"
					/>

					<CustomCheckbox
						label="Ignore TLS Errors"
						bind:checked={modalState.ignoreTLS}
						classes="flex items-center gap-2"
					/>
				</div>
			{/if}

			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2 py-2">
					<Button onclick={newDownload} type="submit" size="sm">Download</Button>
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
				if (isAPIResponse(result) && result.status === 'success') {
					modalState.isDelete = false;
					modalState.title = '';
					activeRows = null;
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete download', { position: 'bottom-center' });
				}
			},
			onCancel: () => {
				modalState.isDelete = false;
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
				if (isAPIResponse(result) && result.status === 'success') {
					modalState.isBulkDelete = false;
					modalState.title = '';
					activeRows = null;
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete downloads', { position: 'bottom-center' });
				}
			},
			onCancel: () => {
				modalState.isBulkDelete = false;
				modalState.title = '';
			}
		}}
	></AlertDialog>
</div>
