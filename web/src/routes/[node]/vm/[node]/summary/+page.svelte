<script lang="ts">
	import { page } from '$app/state';
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';

	import { goto } from '$app/navigation';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import * as Card from '$lib/components/ui/card/index.js';

	import {
		actionVm,
		deleteVM,
		getStats,
		getVmById,
		getVMDomain,
		getVMs,
		updateDescription
	} from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { VM, VMDomain, VMStat } from '$lib/types/vm/vm';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import type { Chart } from 'chart.js';
	import { resource, useInterval } from 'runed';
	import { untrack } from 'svelte';

	interface Data {
		vm: VM;
		domain: VMDomain;
		stats: VMStat[];
	}

	let { data }: { data: Data } = $props();
	const vmId = page.url.pathname.split('/')[3];

	const vm = resource(
		() => 'vm',
		async (key) => {
			const result = await getVmById(Number(vmId), 'vmid');
			updateCache(key, result);
			return result;
		},
		{ lazy: true, initialValue: data.vm }
	);

	const domain = resource(
		() => `vm-domain-${vmId}`,
		async (key) => {
			const result = await getVMDomain(vmId);
			updateCache(key, result);
			return result;
		},
		{ lazy: true, initialValue: data.domain }
	);

	const stats = resource(
		() => `vm-stats-${vmId}`,
		async (key) => {
			const result = await getStats(Number(vmId), 128);
			updateCache(key, result);
			return result;
		},
		{ lazy: true, initialValue: data.stats }
	);

	useInterval(() => 1000, {
		callback: () => {
			if (storage.visible) {
				vm.refetch();
				domain.refetch();
				stats.refetch();
			}
		}
	});

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				vm.refetch();
				domain.refetch();
				stats.refetch();
			});
		}
	});

	let recentStat = $derived(stats.current[stats.current.length - 1] || ({} as VMStat));

	let vmDescription = $derived.by(() => {
		return vm.current.description || '';
	});

	let modalState = $state({
		isDeleteOpen: false,
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

	async function handleDelete() {
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.vmId})</b> is being deleted`;

		await sleep(1000);
		const result = await deleteVM(
			vm.current.id,
			modalState.deleteMACs,
			modalState.deleteRAWDisks,
			modalState.deleteVolumes
		);
		modalState.loading.open = false;
		reload.leftPanel = true;

		if (result.status === 'error') {
			toast.error('Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			goto(`/${storage.hostname}/summary`);
			toast.success('VM deleted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleStart() {
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.vmId})</b> is being started.`;
		modalState.loading.iconColor = 'text-green-500';

		const result = await actionVm(vm.current.id, 'start');

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
	}

	async function handleStop() {
		modalState.loading.open = true;
		modalState.loading.title = 'Stopping Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.vmId})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		const result = await actionVm(vm.current.id, 'stop');

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
	}

	async function handleShutdown() {
		modalState.loading.open = true;
		modalState.loading.title = 'Shutting Down Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.vmId})</b> is being shut down`;
		modalState.loading.iconColor = 'text-yellow-500';

		const result = await actionVm(vm.current.id, 'shutdown');
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
			toast.success('VM shut down', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleReboot() {
		modalState.loading.open = true;
		modalState.loading.title = 'Rebooting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.current.name} (${vm.current.vmId})</b> is being rebooted`;
		modalState.loading.iconColor = 'text-blue-500';

		const result = await actionVm(vm.current.id, 'reboot');
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

	let cpuHistoricalData = $derived.by(() => {
		return {
			field: 'cpuUsage',
			label: 'CPU Usage',
			color: 'chart-1',
			data: stats.current
				.map((data) => ({
					date: new Date(data.createdAt),
					value: Math.floor(data.cpuUsage)
				}))
				.slice(-12)
		};
	});

	let memoryUsageData = $derived.by(() => {
		return {
			field: 'memoryUsage',
			label: 'Memory Usage',
			color: 'chart-2',
			data: stats.current
				.map((data) => ({
					date: new Date(data.createdAt),
					value: Math.floor(data.memoryUsage)
				}))
				.slice(-12)
		};
	});

	$effect(() => {
		if (vmDescription) {
			updateDescription(vm.current.id, vmDescription);
		}
	});

	let cpuUsageRef: Chart | null = $state(null);
	let memoryUsageRef: Chart | null = $state(null);
</script>

{#snippet button(type: string)}
	{#if type === 'start' && domain.current.id == -1 && domain.current.status !== 'Running'}
		<Button
			onclick={() => handleStart()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
			{'Start'}
		</Button>

		<Button
			onclick={() => {
				modalState.isDeleteOpen = true;
				modalState.title = `${vm.current.name} (${vm.current.vmId})`;
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

			{'Delete'}
		</Button>
	{:else if (type === 'stop' || type === 'shutdown' || type === 'reboot') && domain.current.id !== -1 && domain.current.status === 'Running'}
		<Button
			onclick={() =>
				type === 'stop' ? handleStop() : type === 'shutdown' ? handleShutdown() : handleReboot()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
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
	<div class="flex h-10 w-full items-center gap-2 border p-4">
		{@render button('start')}
		{@render button('reboot')}
		{@render button('shutdown')}
		{@render button('stop')}
	</div>

	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full">
			<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
							{`${vm.current.name} ${udTime ? `(${udTime})` : ''}`}
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

			<div class="space-y-4 p-3">
				<AreaChart
					title="CPU Usage"
					elements={[cpuHistoricalData]}
					chart={cpuUsageRef}
					percentage={true}
				/>
				<AreaChart
					title="Memory Usage"
					elements={[memoryUsageData]}
					chart={memoryUsageRef}
					percentage={true}
				/>
			</div>
		</ScrollArea>
	</div>
</div>

<!-- <AlertDialog
	open={modalState.isDeleteOpen}
	names={{ parent: 'VM', element: modalState?.title || '' }}
	actions={{
		onConfirm: async () => {
			handleDelete();
		},
		onCancel: () => {
			modalState.isDeleteOpen = false;
		}
	}}
></AlertDialog> -->

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title>Are you sure?</AlertDialogRaw.Title>
			<AlertDialogRaw.Description>
				{`This will permanently delete VM`}
				<span class="font-semibold">{modalState?.title}.</span>

				<div class="flex flex-row space-x-4">
					<CustomCheckbox
						label="Delete MAC Object(s)"
						bind:checked={modalState.deleteMACs}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>

					<CustomCheckbox
						label="Delete RAW Disk(s)"
						bind:checked={modalState.deleteRAWDisks}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>

					<CustomCheckbox
						label="Delete Volume(s)"
						bind:checked={modalState.deleteVolumes}
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
