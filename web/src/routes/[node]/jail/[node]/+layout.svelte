<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { deleteJail, getSimpleJailById, getJailStateById, jailAction } from '$lib/api/jail/jail';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import { jailPowerSignal, reload } from '$lib/stores/api.svelte';
	import type { JailState, SimpleJail } from '$lib/types/jail/jail';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let ctId = $derived.by(() => {
		const value = Number(page.url.pathname.split('/')[3]);
		return Number.isFinite(value) ? value : 0;
	});

	let jailChildRoute = $derived.by(() => {
		const segments = page.url.pathname.split('/').filter(Boolean);
		const jailIndex = segments.indexOf('jail');
		if (jailIndex === -1) return '';
		return segments[jailIndex + 2] ?? '';
	});
	let isSummaryPage = $derived.by(() => jailChildRoute === '' || jailChildRoute === 'summary');
	let isConsolePage = $derived.by(() => jailChildRoute === 'console');

	const jail = resource(
		() => `simple-jail-${ctId}`,
		async (key: string): Promise<SimpleJail | null> => {
			if (!ctId) return null;
			const result = await getSimpleJailById(ctId, 'ctid');
			updateCache(key, result);
			return result;
		},
		{ initialValue: null as SimpleJail | null }
	);

	const jState = resource(
		() => `jail-${ctId}-state`,
		async (key: string): Promise<JailState | null> => {
			if (!ctId) return null;
			const result = await getJailStateById(ctId);
			updateCache(key, result);
			return result;
		},
		{ initialValue: null as JailState | null }
	);

	const visible = new IsDocumentVisible();

	let modalState = $state({
		isDeleteOpen: false,
		deleteMacs: false,
		deleteRootFS: false,
		title: '',
		loading: {
			open: false,
			title: '',
			description: '',
			iconColor: ''
		}
	});

	async function refreshJailState() {
		await Promise.all([jail.refetch(), jState.refetch()]);
	}

	watch(
		() => ctId,
		(newCtId) => {
			if (newCtId) {
				refreshJailState();
			}
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (visible.current && ctId) {
				jState.refetch();
			}
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle && ctId) {
				refreshJailState();
			}
		}
	);

	function openDeleteModal() {
		if (!jail.current) return;
		modalState.deleteMacs = false;
		modalState.deleteRootFS = false;
		modalState.title = `${jail.current.name} (${jail.current.ctId})`;
		modalState.isDeleteOpen = true;
	}

	async function handleDelete() {
		if (!jail.current) return;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being deleted`;
		modalState.loading.iconColor = 'text-red-500';

		await sleep(1000);
		const result = await deleteJail(
			jail.current.ctId,
			modalState.deleteMacs,
			modalState.deleteRootFS
		);
		reload.leftPanel = true;
		modalState.loading.open = false;

		if (result.status === 'error') {
			toast.error('Error deleting jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await goto(`/${storage.hostname}/summary`);
			toast.success('Jail deleted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleStop() {
		if (!jail.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Stopping Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		await sleep(1000);
		const result = await jailAction(jail.current.ctId, 'stop');
		reload.leftPanel = true;
		modalState.loading.open = false;

		if (result.status === 'error') {
			toast.error('Error stopping jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			jailPowerSignal.token += 1;
			jailPowerSignal.ctId = jail.current.ctId;
			jailPowerSignal.action = 'stop';

			toast.success('Jail stopped', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshJailState();
	}

	async function handleStart() {
		if (!jail.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being started`;
		modalState.loading.iconColor = 'text-green-500';

		await sleep(1000);
		const result = await jailAction(jail.current.ctId, 'start');
		reload.leftPanel = true;
		modalState.loading.open = false;

		if (result.status === 'error') {
			toast.error('Error starting jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			jailPowerSignal.token += 1;
			jailPowerSignal.ctId = jail.current.ctId;
			jailPowerSignal.action = 'start';

			toast.success('Jail started', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshJailState();
	}
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	{#if !isSummaryPage}
		<div class="flex h-10 w-full shrink-0 items-center justify-between gap-1 border p-4">
			<div class="min-w-0 flex items-center gap-2">
				{#if jail.current && jState.current}
					<Badge
						variant="outline"
						class="text-muted-foreground px-1.5"
						title={jState.current.state}
					>
						{#if jState.current.state === 'ACTIVE'}
							<span class="icon-[mdi--check-circle] text-green-500"></span>
						{:else}
							<span class="icon-[mdi--close-circle] text-gray-500"></span>
						{/if}
					</Badge>
					<p class="truncate text-sm font-semibold">{jail.current.name} ({jail.current.ctId})</p>
				{/if}
			</div>

			<div class="flex items-center gap-1">
				{#if jail.current && jState.current}
					{#if jState.current.state === 'ACTIVE'}
						<Button
							onclick={handleStop}
							size="sm"
							class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
						>
							<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
							{'Stop'}
						</Button>
					{:else}
						<Button
							onclick={handleStart}
							size="sm"
							class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
						>
							<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
							{'Start'}
						</Button>

						<Button
							onclick={openDeleteModal}
							size="sm"
							class="ml-2 bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
						>
							<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
							{'Delete'}
						</Button>
					{/if}
				{/if}
			</div>
		</div>
	{/if}

	<div
		class="min-h-0 flex-1"
		class:overflow-hidden={isConsolePage}
		class:overflow-auto={!isConsolePage}
	>
		{@render children?.()}
	</div>
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title>Are you sure?</AlertDialogRaw.Title>
			<AlertDialogRaw.Description>
				{`This will permanently delete Jail`}
				<span class="font-semibold">{modalState?.title}.</span>
				<div class="flex flex-row gap-2">
					<CustomCheckbox
						label="Delete MAC Object(s)"
						bind:checked={modalState.deleteMacs}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>
					<CustomCheckbox
						label="Delete Root Filesystem"
						bind:checked={modalState.deleteRootFS}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>
				</div>
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}>Continue</AlertDialogRaw.Action>
		</AlertDialogRaw.Footer>
	</AlertDialogRaw.Content>
</AlertDialogRaw.Root>

<LoadingDialog
	bind:open={modalState.loading.open}
	title={modalState.loading.title}
	description={modalState.loading.description}
	iconColor={modalState.loading.iconColor}
/>
