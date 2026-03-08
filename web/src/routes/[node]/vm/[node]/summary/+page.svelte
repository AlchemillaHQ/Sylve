<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card/index.js';
	import { getActiveLifecycleTaskForGuest } from '$lib/api/task/lifecycle';
	import {
		actionVm,
		deleteVM,
		getStats,
		getVmById,
		getVMDomain,
		updateDescription
	} from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import {
		VMStatSchema,
		type QGAInfo,
		type VM,
		type VMDomain,
		type VMStat
	} from '$lib/types/vm/vm';
	import { getObjectSchemaDefaults, sleep } from '$lib/utils';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { resource, useInterval, Debounced, IsDocumentVisible, watch } from 'runed';
	import type { APIResponse, GFSStep } from '$lib/types/common';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import { getVMIconByGaId } from '$lib/utils/vm/vm';
	import GuestAgent from '$lib/components/custom/VM/Summary/GuestAgent.svelte';

	interface Data {
		rid: number;
		vm: VM;
		domain: VMDomain;
		stats: VMStat[];
		gaInfo: QGAInfo | APIResponse;
	}

	let { data }: { data: Data } = $props();
	let gfsStep = $state<GFSStep>('hourly');

	// svelte-ignore state_referenced_locally
	const vm = resource(
		() => 'vm-' + data.vm.rid,
		async (key) => {
			const result = await getVmById(Number(data.vm.rid), 'rid');
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.vm }
	);

	// svelte-ignore state_referenced_locally
	const domain = resource(
		() => `vm-domain-${data.vm.rid}`,
		async (key) => {
			const result = await getVMDomain(data.vm.rid);
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.domain }
	);

	const lifecycleTask = resource(
		() => `vm-lifecycle-task-${data.vm.rid}`,
		async () => {
			return await getActiveLifecycleTaskForGuest('vm', data.vm.rid);
		},
		{ initialValue: null as LifecycleTask | null }
	);

	// svelte-ignore state_referenced_locally
	const stats = resource(
		[() => gfsStep],
		async ([gfsStep]) => {
			const result = await getStats(Number(data.vm.id), gfsStep);
			const key = `vm-stats-${data.vm.id}`;
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.stats }
	);

	const visible = new IsDocumentVisible();

	useInterval(() => 1000, {
		callback: () => {
			if (visible.current) {
				domain.refetch();
			}
		}
	});

	useInterval(() => 1500, {
		callback: () => {
			if (visible.current) {
				lifecycleTask.refetch();
			}
		}
	});

	useInterval(() => 3000, {
		callback: () => {
			if (visible.current) {
				stats.refetch();
			}
		}
	});

	watch(
		() => domain.current,
		(currentDomain, prevDomain) => {
			if (prevDomain?.status !== currentDomain.status) {
				vm.refetch();
			}
		}
	);

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				vm.refetch();
				domain.refetch();
				stats.refetch();
				lifecycleTask.refetch();
			}
		}
	);

	let recentStat = $derived(
		stats.current[stats.current.length - 1] || getObjectSchemaDefaults(VMStatSchema)
	);
	let gaRefreshSignal = $state(0);
	let initialGaInfo = $derived.by(() => (isAPIResponse(data.gaInfo) ? null : data.gaInfo));

	let vmDescription = $state(vm.current.description || '');
	let debouncedDesc = new Debounced(() => vmDescription, 500);
	let isDescInitialized = false;

	watch(
		() => debouncedDesc.current,
		(curr, prev) => {
			if (!isDescInitialized) {
				isDescInitialized = true;
				return;
			}

			if (curr !== undefined && prev !== undefined) {
				if (curr !== prev) {
					updateDescription(vm.current.rid, curr);
				}
			}
		}
	);

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

	function openDeleteModal(forceDelete: boolean = false) {
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	async function handleDelete() {
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
			goto(`/${storage.hostname}/summary`);
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
		const result = await actionVm(vm.current.rid, 'start');

		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error(result.message === 'lifecycle_task_in_progress' ? 'VM action already in progress' : 'Error starting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			toast.success('VM start queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		gaRefreshSignal += 1;
		lifecycleTask.refetch();
	}

	async function handleStop() {
		const result = await actionVm(vm.current.rid, 'stop');

		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error(result.message === 'lifecycle_task_in_progress' ? 'VM action already in progress' : 'Error stopping VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			if (result.message === 'vm_force_stop_requested') {
				toast.warning('Force stop requested', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else {
				toast.success('VM stop queued', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		}

		lifecycleTask.refetch();
	}

	async function handleForceStop() {
		const result = await actionVm(vm.current.rid, 'stop');
		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error(result.message === 'lifecycle_task_in_progress' ? 'VM action already in progress' : 'Error requesting force stop', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else {
			toast.warning('Force stop requested', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		lifecycleTask.refetch();
	}

	async function handleShutdown() {
		const result = await actionVm(vm.current.rid, 'shutdown');
		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error(result.message === 'lifecycle_task_in_progress' ? 'VM action already in progress' : 'Error shutting down VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			toast.success('VM shutdown queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		lifecycleTask.refetch();
	}

	async function handleReboot() {
		const result = await actionVm(vm.current.rid, 'reboot');
		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error(result.message === 'lifecycle_task_in_progress' ? 'VM action already in progress' : 'Error rebooting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			toast.success('VM reboot queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		gaRefreshSignal += 1;
		lifecycleTask.refetch();
	}

	let udTime = $derived.by(() => {
		if (domain.current.status === 'Running') {
			if (vm.current.startedAt) {
				return `Started ${dateToAgo(vm.current.startedAt)}`;
			}
		} else if (domain.current.status === 'Stopped' || domain.current.status === 'Shutoff') {
			if (vm.current.stoppedAt) {
				return `Stopped ${dateToAgo(vm.current.stoppedAt)}`;
			}
		}
		return '';
	});

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);
	let isDomainErrorState = $derived.by(() => normalizedDomainStatus === 'error');
	let hasActiveLifecycleTask = $derived(!!lifecycleTask.current);
	let activeLifecycleAction = $derived(lifecycleTask.current?.action || '');
	let isShutdownTaskActive = $derived.by(
		() => lifecycleTask.current?.action === 'shutdown' && !lifecycleTask.current?.overrideRequested
	);
</script>

{#snippet button(type: string)}
	{#if type === 'start' && domain.current.id == -1 && normalizedDomainStatus !== 'running' && !isDomainErrorState}
		<Button
			onclick={() => handleStart()}
			disabled={hasActiveLifecycleTask}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
			{'Start'}
		</Button>

		<Button
			onclick={() => openDeleteModal(false)}
			disabled={hasActiveLifecycleTask}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

			{'Delete'}
		</Button>
	{:else if type === 'force-delete' && isDomainErrorState}
		<Button
			onclick={() => openDeleteModal(true)}
			disabled={hasActiveLifecycleTask}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-700 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--alert-octagon] mr-1 h-4 w-4"></span>
			{'Force Delete'}
		</Button>
	{:else if type === 'force-stop' && domain.current.id !== -1 && domain.current.status === 'Running' && isShutdownTaskActive}
		<Button
			onclick={() => handleForceStop()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--alert] mr-1 h-4 w-4"></span>
				<span>Force Stop</span>
			</div>
		</Button>
	{:else if (type === 'stop' || type === 'shutdown' || type === 'reboot') && domain.current.id !== -1 && domain.current.status === 'Running'}
		<Button
			onclick={() =>
				type === 'stop' ? handleStop() : type === 'shutdown' ? handleShutdown() : handleReboot()}
			disabled={hasActiveLifecycleTask}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			{#if type === 'stop'}
				<div class="flex items-center">
					<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
					<span>Stop</span>
				</div>
			{:else if type === 'shutdown'}
				<div class="flex items-center">
					<span class="icon-[mdi--power] mr-1 h-4 w-4"></span>
					<span>Shutdown</span>
				</div>
			{:else if type === 'reboot'}
				<div class="flex items-center">
					<span class="icon-[mdi--restart] mr-1 h-4 w-4"></span>
					<span>Reboot</span>
				</div>
			{/if}
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-1 border p-4">
		{@render button('start')}
		{@render button('force-delete')}
		{@render button('force-stop')}
		{@render button('reboot')}
		{@render button('shutdown')}
		{@render button('stop')}
		{#if hasActiveLifecycleTask}
			<span class="ml-1 inline-flex items-center text-xs text-muted-foreground">
				<span class="icon-[mdi--loading] mr-1 h-3 w-3 animate-spin"></span>
				{activeLifecycleAction}
			</span>
		{/if}

		<!-- Towards the right we should have another button -->
		<div class="ml-auto flex h-full items-center">
			<SimpleSelect
				options={[
					{ label: 'Hourly', value: 'hourly' },
					{ label: 'Daily', value: 'daily' },
					{ label: 'Weekly', value: 'weekly' },
					{ label: 'Monthly', value: 'monthly' },
					{ label: 'Yearly', value: 'yearly' }
				]}
				bind:value={gfsStep}
				onChange={() => {
					stats.refetch();
				}}
				classes={{ trigger: 'h-6!' }}
				icon="icon-[mdi--calendar]"
			/>
		</div>
	</div>

	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full">
			<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
							<div class="flex items-center gap-1.5 whitespace-nowrap">
								{#if initialGaInfo && getVMIconByGaId(initialGaInfo.osInfo.id || '')}
									<span class="icon {getVMIconByGaId(initialGaInfo.osInfo.id || '')} h-6 w-6"
									></span>
								{/if}
								<span>{vm.current.name}</span>
								{#if udTime}
									<span>({udTime})</span>
								{/if}
							</div>
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<div class="flex items-start">
							<div class="flex items-center">
								<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
								{'Status'}
							</div>
							<div class="ml-auto">
								{domain.current.status}
							</div>
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>

									{'CPU Usage'}
								</p>
								<p class="ml-auto">
									{#if domain.current.status === 'Running'}
										{`${floatToNDecimals(recentStat.cpuUsage, 2)}% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
									{:else}
										{`0% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
									{/if}
								</p>
							</div>

							{#if domain.current.status === 'Running'}
								<Progress value={recentStat.cpuUsage || 0} max={100} class="ml-auto h-2" />
							{:else}
								<Progress value={0} max={100} class="ml-auto h-2" />
							{/if}
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[ph--memory] mr-1 h-5 w-5"></span>

									{'RAM Usage'}
								</p>
								<p class="ml-auto">
									{#if vm}
										{#if domain.current.status === 'Running'}
											{`${floatToNDecimals(recentStat.memoryUsage, 2)}% of ${humanFormat(vm.current.ram || 0)}`}
										{:else}
											{`0% of ${humanFormat(vm.current.ram || 0)}`}
										{/if}
									{/if}
								</p>
							</div>

							{#if domain.current.status === 'Running'}
								<Progress value={recentStat.memoryUsage || 0} max={100} class="ml-auto h-2" />
							{:else}
								<Progress value={0} max={100} class="ml-auto h-2" />
							{/if}
						</div>
					</Card.Content>
				</Card.Root>

				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
							Description
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<CustomValueInput
							label={''}
							placeholder="Notes about VM"
							bind:value={vmDescription}
							classes=""
							textAreaClasses="!h-32"
							type="textarea"
						/>
					</Card.Content>
				</Card.Root>
			</div>

			<GuestAgent rid={data.vm.rid} initialGaInfo={data.gaInfo} refreshSignal={gaRefreshSignal} />

			<div class="space-y-4 px-4 pb-4">
				<LineBrush
					title="CPU Usage"
					points={stats.current.map((data) => ({
						date: new Date(data.createdAt).getTime(),
						value: Number(data.cpuUsage)
					}))}
					percentage={true}
					color="one"
					containerContentHeight="h-64"
					titleIconClass="icon-[solar--cpu-bold]"
				/>

				<LineBrush
					title="Memory Usage"
					points={stats.current.map((data) => ({
						date: new Date(data.createdAt).getTime(),
						value: Number(data.memoryUsage)
					}))}
					percentage={true}
					color="two"
					containerContentHeight="h-64"
					titleIconClass="icon-[ph--memory]"
				/>
			</div>
		</ScrollArea>
	</div>
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
