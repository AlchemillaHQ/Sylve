<script lang="ts">
	import { page } from '$app/state';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';

	import { goto } from '$app/navigation';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import * as Card from '$lib/components/ui/card/index.js';

	import {
		actionVm,
		deleteVM,
		getStats,
		getVMDomain,
		getVMs,
		updateDescription
	} from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';

	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';

	import { hostname } from '$lib/stores/basic';
	import type { VM, VMDomain, VMStat } from '$lib/types/vm/vm';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';

	import LineGraph from '$lib/components/custom/Charts/LineGraph.svelte';
	import type { HistoricalData } from '$lib/types/common';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { capitalizeFirstLetter } from '$lib/utils/string';
	import { dateToAgo } from '$lib/utils/time';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';

	interface Data {
		vms: VM[];
		domain: VMDomain;
		stats: VMStat[];
	}

	let { data }: { data: Data } = $props();
	const vmId = page.url.pathname.split('/')[3];

	const results = useQueries([
		{
			queryKey: ['vm-list'],
			queryFn: async () => {
				return await getVMs();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.vms,
			onSuccess: (data: VM[]) => {
				updateCache('vm-list', data);
			}
		},
		{
			queryKey: [`vm-domain-${vmId}`],
			queryFn: async () => {
				return await getVMDomain(vmId);
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.domain,
			onSuccess: (data: VMDomain) => {
				updateCache(`vm-domain-${vmId}`, data);
			}
		},
		{
			queryKey: [`vm-stats-${vmId}`],
			queryFn: async () => {
				return await getStats(Number(vmId), 128);
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.stats,
			onSuccess: (data: VMStat[]) => {
				updateCache(`vm-stats-${vmId}`, data);
			}
		}
	]);

	let domain: VMDomain = $derived($results[1].data as VMDomain);
	let vm: VM = $derived(
		($results[0].data as VM[]).find((vm: VM) => vm.vmId === parseInt(vmId)) || ({} as VM)
	);
	let stats: VMStat[] = $derived($results[2].data as VMStat[]);
	let recentStat = $derived(stats[0] || ({} as VMStat));

	let vmDescription = $derived.by(() => {
		return vm.description || '';
	});

	let modalState = $state({
		isDeleteOpen: false,
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
		modalState.loading.description = `Please wait while VM <b>${vm.name} (${vm.vmId})</b> is being deleted`;

		await sleep(1000);
		const result = await deleteVM(vm.id);
		modalState.loading.open = false;

		if (result.status === 'error') {
			toast.error('Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			goto(`/${$hostname}/summary`);
			toast.success('VM deleted', {
				duration: 5000,
				position: 'bottom-center'
			});
		}
	}

	async function handleStart() {
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Virtual Machine';
		modalState.loading.description = `Please wait while VM <b>${vm.name} (${vm.vmId})</b> is being started.`;
		modalState.loading.iconColor = 'text-green-500';

		const result = await actionVm(vm.id, 'start');
		console.log('Start VM result:', result);

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
		modalState.loading.description = `Please wait while VM <b>${vm.name} (${vm.vmId})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		const result = await actionVm(vm.id, 'stop');

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

	let udTime = $derived.by(() => {
		if (domain.status === 'Running') {
			if (vm.startedAt) {
				return `Started ${dateToAgo(vm.startedAt)}`;
			}
		} else if (domain.status === 'Stopped' || domain.status === 'Shutoff') {
			if (vm.stoppedAt) {
				return `Stopped ${dateToAgo(vm.stoppedAt)}`;
			}
		}
		return '';
	});

	let cpuHistoricalData = $derived.by(() => {
		return {
			field: 'cpuUsage',
			label: 'CPU Usage',
			color: 'chart-1',
			data: stats
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
			data: stats
				.map((data) => ({
					date: new Date(data.createdAt),
					value: Math.floor(data.memoryUsage)
				}))
				.slice(-12)
		};
	});

	$effect(() => {
		if (vmDescription) {
			updateDescription(vm.id, vmDescription);
		}
	});
</script>

{#snippet button(type: string)}
	{#if type === 'start' && domain.id == -1 && domain.status !== 'Running'}
		<Button
			onclick={() => handleStart()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<Icon icon="mdi:play" class="mr-1 h-4 w-4" />
			{'Start'}
		</Button>

		<Button
			onclick={() => {
				modalState.isDeleteOpen = true;
				modalState.title = `${vm.name} (${vm.vmId})`;
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<Icon icon="mdi:delete" class="mr-1 h-4 w-4" />
			{'Delete'}
		</Button>
	{:else if type === 'stop' && domain.id !== -1 && domain.status === 'Running'}
		<Button
			onclick={() => handleStop()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<Icon icon="mdi:stop" class="mr-1 h-4 w-4" />
			{'Stop'}
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-4">
		{@render button('start')}
		{@render button('stop')}
	</div>

	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full">
			<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
							{`${vm.name} ${udTime ? `(${udTime})` : ''}`}
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<div class="flex items-start">
							<div class="flex items-center">
								<Icon icon="fluent:status-12-filled" class="mr-1 h-5 w-5" />
								{'Status'}
							</div>
							<div class="ml-auto">
								{domain.status}
							</div>
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<Icon icon="solar:cpu-bold" class="mr-1 h-5 w-5" />
									{'CPU Usage'}
								</p>
								<p class="ml-auto">
									{#if domain.status === 'Running'}
										{`${floatToNDecimals(recentStat.cpuUsage, 2)}% of ${vm.cpuCores * vm.cpuThreads * vm.cpuSockets} vCPU(s)`}
									{:else}
										{`0% of ${vm.cpuCores * vm.cpuThreads * vm.cpuSockets} vCPU(s)`}
									{/if}
								</p>
							</div>

							{#if domain.status === 'Running'}
								<Progress value={recentStat.cpuUsage || 0} max={100} class="ml-auto h-2" />
							{:else}
								<Progress value={0} max={100} class="ml-auto h-2" />
							{/if}
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<Icon icon="ph:memory" class="mr-1 h-5 w-5" />
									{'RAM Usage'}
								</p>
								<p class="ml-auto">
									{#if vm}
										{#if domain.status === 'Running'}
											{`${floatToNDecimals(recentStat.memoryUsage, 2)}% of ${humanFormat(vm.ram || 0)}`}
										{:else}
											{`0% of ${humanFormat(vm.ram || 0)}`}
										{/if}
									{/if}
								</p>
							</div>

							{#if domain.status === 'Running'}
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
							textAreaCLasses="!h-32"
							type="textarea"
						/>
					</Card.Content>
				</Card.Root>
			</div>

			<div class="space-y-4 p-3">
				<AreaChart title="CPU Usage" elements={[cpuHistoricalData]} />
				<AreaChart title="Memory Usage" elements={[memoryUsageData]} />
			</div>
		</ScrollArea>
	</div>
</div>

<AlertDialog
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
></AlertDialog>

<LoadingDialog
	bind:open={modalState.loading.open}
	title={modalState.loading.title}
	description={modalState.loading.description}
	iconColor={modalState.loading.iconColor}
/>
