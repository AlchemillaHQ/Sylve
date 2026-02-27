<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { actionVm, deleteVM, getSimpleVMById, getVMDomain } from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import { reload } from '$lib/stores/api.svelte';
	import { sleep } from '$lib/utils';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { SimpleVm, VMDomain } from '$lib/types/vm/vm';
	import { isAPIResponse, updateCache } from '$lib/utils/http';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let rid = $derived.by(() => {
		const value = Number(page.url.pathname.split('/')[3]);
		return Number.isFinite(value) ? value : 0;
	});

	const vm = resource(
		() => `simple-vm-${rid}`,
		async (key: string): Promise<SimpleVm | null> => {
			if (!rid) return null;
			const result = await getSimpleVMById(rid, 'rid');
			if (isAPIResponse(result)) {
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: null as SimpleVm | null }
	);

	const domain = resource(
		() => `vm-domain-${rid}`,
		async (key: string): Promise<VMDomain | null> => {
			if (!rid) return null;
			const result = await getVMDomain(rid);
			if (isAPIResponse(result)) {
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: null as VMDomain | null }
	);

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);

	let isDomainErrorState = $derived.by(() => normalizedDomainStatus === 'error');
	let isSummaryPage = $derived.by(() => page.url.pathname.endsWith('/summary'));

	const visible = new IsDocumentVisible();

	let modalState = $state({
		isDeleteOpen: false,
		forceDelete: false,
		deleteMACs: true,
		deleteRAWDisks: false,
		deleteVolumes: false,
		title: '',
		loading: {
			open: false,
			title: '',
			description: '',
			iconColor: ''
		}
	});

	async function refreshVmDomain() {
		await Promise.all([vm.refetch(), domain.refetch()]);
	}

	watch(
		() => rid,
		(newRid) => {
			if (newRid) {
				refreshVmDomain();
			}
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (visible.current && rid) {
				domain.refetch();
			}
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle && rid) {
				refreshVmDomain();
			}
		}
	);

	function openDeleteModal(forceDelete: boolean = false) {
		if (!vm.current) return;
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	async function handleDelete() {
		if (!vm.current) return;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = modalState.forceDelete
			? 'Force Deleting Virtual Machine'
			: 'Deleting Virtual Machine';
		modalState.loading.description = modalState.forceDelete
			? `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being force deleted with best-effort cleanup`
			: `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being deleted`;

		await sleep(1000);
		const result = await deleteVM(
			vm.current.rid,
			modalState.deleteMACs,
			modalState.deleteRAWDisks,
			modalState.deleteVolumes,
			modalState.forceDelete
		);
		modalState.loading.open = false;
		reload.leftPanel = true;
		const wasForceDelete = modalState.forceDelete;
		modalState.forceDelete = false;

		if (result.status === 'error') {
			toast.error(wasForceDelete ? 'Error force deleting VM' : 'Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await goto(`/${storage.hostname}/summary`);
			if (wasForceDelete && result.message === 'vm_force_removed_with_warnings') {
				toast.warning('VM force deleted with warnings', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else {
				toast.success(wasForceDelete ? 'VM force deleted' : 'VM deleted', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		}
	}

	async function handleStart() {
		if (!vm.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being started.`;
		modalState.loading.iconColor = 'text-green-500';

		const result = await actionVm(vm.current.rid, 'start');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error starting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM started', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleStop() {
		if (!vm.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Stopping Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		const result = await actionVm(vm.current.rid, 'stop');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error stopping VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM stopped', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleShutdown() {
		if (!vm.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Shutting Down Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is shutting down`;
		modalState.loading.iconColor = 'text-yellow-500';

		const result = await actionVm(vm.current.rid, 'shutdown');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error shutting down VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM shutdown started', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleReboot() {
		if (!vm.current) return;
		modalState.loading.open = true;
		modalState.loading.title = 'Rebooting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being rebooted`;
		modalState.loading.iconColor = 'text-yellow-500';

		const result = await actionVm(vm.current.rid, 'reboot');
		reload.leftPanel = true;

		if (result.status === 'error') {
			modalState.loading.open = false;
			toast.error('Error rebooting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await sleep(1000);
			modalState.loading.open = false;
			toast.success('VM rebooted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}
</script>

<div class="h-full w-full overflow-auto">
	{#if !isSummaryPage}
		<div class="flex h-10 w-full items-center gap-1 border p-4">
			{#if vm.current && domain.current}
				{#if domain.current.id === -1 && normalizedDomainStatus !== 'running' && !isDomainErrorState}
					<Button
						onclick={() => handleStart()}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
						{'Start'}
					</Button>

					<Button
						onclick={() => openDeleteModal(false)}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
						{'Delete'}
					</Button>
				{/if}

				{#if isDomainErrorState}
					<Button
						onclick={() => openDeleteModal(true)}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-700 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<span class="icon-[mdi--alert-octagon] mr-1 h-4 w-4"></span>
						{'Force Delete'}
					</Button>
				{/if}

				{#if domain.current.id !== -1 && domain.current.status === 'Running'}
					<Button
						onclick={() => handleReboot()}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<div class="flex items-center">
							<span class="icon-[mdi--restart] mr-1 h-4 w-4"></span>
							<span>Reboot</span>
						</div>
					</Button>

					<Button
						onclick={() => handleShutdown()}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<div class="flex items-center">
							<span class="icon-[mdi--power] mr-1 h-4 w-4"></span>
							<span>Shutdown</span>
						</div>
					</Button>

					<Button
						onclick={() => handleStop()}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<div class="flex items-center">
							<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
							<span>Stop</span>
						</div>
					</Button>
				{/if}
			{/if}
		</div>
	{/if}

	{@render children?.()}
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5 max-w-xl!">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title
				>{modalState.forceDelete ? 'Force Delete VM?' : 'Are you sure?'}</AlertDialogRaw.Title
			>
			<AlertDialogRaw.Description>
				{modalState.forceDelete ? `This will force delete VM` : `This will permanently delete VM`}
				<span class="font-semibold">{modalState?.title}.</span>
				{#if modalState.forceDelete}
					<div class="mt-2 text-sm">
						Best-effort cleanup will attempt libvirt/domain removal, VM datasets, VM DB records, and
						VM network objects. Partial failures will be tolerated.
					</div>
				{:else}
					<div class="flex flex-row items-center gap-6 mt-1 whitespace-nowrap">
						<CustomCheckbox
							label="Delete MAC Object(s)"
							bind:checked={modalState.deleteMACs}
							classes="flex items-center gap-2 mt-3"
						></CustomCheckbox>

						<CustomCheckbox
							label="Delete RAW Disk(s)"
							bind:checked={modalState.deleteRAWDisks}
							classes="flex items-center gap-2 mt-3"
						></CustomCheckbox>

						<CustomCheckbox
							label="Delete Volume(s)"
							bind:checked={modalState.deleteVolumes}
							classes="flex items-center gap-2 mt-3"
						></CustomCheckbox>
					</div>
				{/if}
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
					modalState.forceDelete = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}
				>{modalState.forceDelete ? 'Force Delete' : 'Continue'}</AlertDialogRaw.Action
			>
		</AlertDialogRaw.Footer>
	</AlertDialogRaw.Content>
</AlertDialogRaw.Root>

<LoadingDialog
	bind:open={modalState.loading.open}
	title={modalState.loading.title}
	description={modalState.loading.description}
	iconColor={modalState.loading.iconColor}
/>
