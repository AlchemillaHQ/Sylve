<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo } from '$lib/api/info/ram';
	import {
		deleteJail,
		getJailLogs,
		getJails,
		getJailStates,
		getStats,
		jailAction,
		updateDescription
	} from '$lib/api/jail/jail';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { hostname } from '$lib/stores/basic';
	import type { CPUInfo, CPUInfoHistorical } from '$lib/types/info/cpu';
	import type { RAMInfo, RAMInfoHistorical } from '$lib/types/info/ram';
	import type { Jail, JailStat, JailState } from '$lib/types/jail/jail';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';
	import { cleanStats } from '$lib/utils/jail/stats';
	import { dateToAgo } from '$lib/utils/time';
	import { createQueries } from '@tanstack/svelte-query';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';

	interface Data {
		jails: Jail[];
		jailStates: JailState[];
		jail: Jail;
		stats: JailStat[];
	}

	let { data }: { data: Data } = $props();
	const ctId = page.url.pathname.split('/')[3];

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

	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['jail-list'],
				queryFn: async () => {
					return await getJails();
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: data.jails,
				onSuccess: (data: Jail[]) => {
					updateCache('jail-list', data);
				}
			},
			{
				queryKey: ['jail-states'],
				queryFn: async () => {
					return await getJailStates();
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: data.jailStates,
				onSuccess: (data: JailState[]) => {
					updateCache('jail-states', data);
				}
			},
			{
				queryKey: [`jail-${ctId}-start-logs`],
				queryFn: async () => {
					return await getJailLogs(data.jail.id, true);
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: { logs: '' },
				onSuccess: (data: any) => {
					updateCache(`jail-${ctId}-start-logs`, data);
				}
			},
			{
				queryKey: [`jail-${ctId}-stop-logs`],
				queryFn: async () => {
					return await getJailLogs(data.jail.id, false);
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: { logs: '' },
				onSuccess: (data: any) => {
					updateCache(`jail-${ctId}-stop-logs`, data);
				}
			},
			{
				queryKey: [`jail-stats-${ctId}`],
				queryFn: async () => {
					return await getStats(Number(ctId), 128);
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: data.stats,
				onSuccess: (data: JailStat[]) => {
					updateCache(`jail-stats-${ctId}`, data);
				}
			},
			{
				queryKey: ['cpuInfo'],
				queryFn: getCPUInfo,
				refetchInterval: false,
				keepPreviousData: true,
				onSuccess: (data: CPUInfo | CPUInfoHistorical) => {
					updateCache('cpuInfo', data as CPUInfo);
				},
				refetchOnMount: true,
				refetchOnWindowFocus: true
			},
			{
				queryKey: ['ramInfo'],
				queryFn: getRAMInfo,
				refetchInterval: false,
				keepPreviousData: true,
				onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
					updateCache('ramInfo', data);
				},
				refetchOnMount: true,
				refetchOnWindowFocus: true
			}
		]
	}));

	let jail: Jail = $derived(
		(results[0].data as Jail[]).find((jail: Jail) => jail.ctId === parseInt(ctId)) || ({} as Jail)
	);

	let jState: JailState = $derived(
		(results[1].data as JailState[]).find((state: JailState) => state.ctId === parseInt(ctId)) ||
			({} as JailState)
	);

	let startLogs = $derived(results[2].data);
	let stopLogs = $derived(results[3].data);
	let jailDesc = $state(jail.description || '');
	let jailStats = $derived(results[4].data || []);
	let cpuInfo = $derived(results[5].data as CPUInfo);
	let ramInfo = $derived(results[6].data as RAMInfo);
	let logicalCores = $derived(cpuInfo?.logicalCores ?? 0);
	let totalRAM = $derived(ramInfo?.total ?? 0);
	let cpuHistoricalData = $derived.by(() => {
		return {
			field: 'cpuUsage',
			label: 'CPU Usage',
			color: 'chart-1',
			data: cleanStats(jailStats, jail).cpu.slice(-12)
		};
	});

	let memoryHistoricalData = $derived.by(() => {
		return {
			field: 'memoryUsage',
			label: 'Memory Usage',
			color: 'chart-2',
			data: cleanStats(jailStats, jail).memory.slice(-12)
		};
	});

	$effect(() => {
		if (jailDesc) {
			updateDescription(jail.id, jailDesc);
		}
	});

	let udTime = $derived.by(() => {
		if (jState.state === 'ACTIVE') {
			if (jail.startedAt) {
				return `Started ${dateToAgo(jail.startedAt)}`;
			}
		} else if (jState.state === 'INACTIVE' || jState.state === 'UNKNOWN') {
			if (jail.stoppedAt) {
				return `Stopped ${dateToAgo(jail.stoppedAt)}`;
			}
		}
		return '';
	});

	async function handleDelete() {
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.name} (${jail.ctId})</b> is being deleted`;
		modalState.loading.open = false;

		const result = await deleteJail(jail.ctId, modalState.deleteMacs, modalState.deleteRootFS);
		reload.leftPanel = true;
		modalState.loading.open = false;

		if (result.status === 'error') {
			toast.error('Error deleting jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			goto(`/${$hostname}/summary`);
			toast.success('Jail deleted', {
				duration: 5000,
				position: 'bottom-center'
			});

			modalState.isDeleteOpen = false;
		}
	}

	async function handleStop() {
		modalState.loading.open = true;
		modalState.loading.title = 'Stopping Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.name} (${jail.ctId})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		await sleep(1000);
		await jailAction(jail.ctId, 'stop');

		reload.leftPanel = true;
		modalState.loading.open = false;
	}

	async function handleStart() {
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.name} (${jail.ctId})</b> is being started`;
		modalState.loading.iconColor = 'text-green-500';

		await sleep(1000);
		await jailAction(jail.ctId, 'start');

		reload.leftPanel = true;
		modalState.loading.open = false;
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-4">
		{#if jState?.state === 'ACTIVE'}
			<Button
				onclick={handleStop}
				size="sm"
				class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
			>
				<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>

				{'Stop'}
			</Button>
		{:else}
			<div class="flex items-center gap-2">
				<Button
					onclick={handleStart}
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
				>
					<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
					{'Start'}
				</Button>

				<Button
					onclick={() => {
						modalState.isDeleteOpen = true;
					}}
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
				>
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					{'Delete'}
				</Button>
			</div>
		{/if}
	</div>

	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full">
			<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
				<Card.Root class="w-full gap-0 p-4">
					<Card.Header class="p-0">
						<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
							{`${jail?.name} ${udTime ? `(${udTime})` : ''}`}
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<div class="flex items-start">
							<div class="flex items-center">
								<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
								{'Status'}
							</div>
							<div class="ml-auto">
								{jState.state === 'ACTIVE'
									? 'Running'
									: jState.state === 'INACTIVE'
										? 'Stopped'
										: jState.state}
							</div>
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>
									{'CPU Usage'}
								</p>
								<p class="ml-auto">
									{#if jState.state === 'ACTIVE'}
										{`${jState.pcpu.toFixed(2)}% of ${jail.cores || logicalCores} Core(s)`}
									{:else}
										{`0% of ${jail.cores || logicalCores} Core(s)`}
									{/if}
								</p>
							</div>

							{#if jState.state === 'ACTIVE'}
								<Progress value={jState.pcpu} max={100} class="ml-auto h-2" />
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
									{#if jState.state === 'ACTIVE'}
										{`${((jState.memory / (jail.memory || totalRAM)) * 100).toFixed(2)}% of ${humanFormat(jail.memory || totalRAM)}`}
									{:else}
										{`0% of ${humanFormat(jail.memory || totalRAM)}`}
									{/if}
								</p>
							</div>

							{#if jState.state === 'ACTIVE'}
								<Progress
									value={(jState.memory / (jail.memory || totalRAM)) * 100}
									max={100}
									class="ml-auto h-2"
								/>
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
							placeholder="Notes"
							bind:value={jailDesc}
							classes=""
							textAreaClasses="!h-32"
							type="textarea"
						/>
					</Card.Content>
				</Card.Root>
			</div>

			<div class="space-y-4 p-3">
				<AreaChart title="CPU Usage" elements={[cpuHistoricalData]} percentage={true} />
				<AreaChart title="Memory Usage" elements={[memoryHistoricalData]} percentage={true} />
			</div>
		</ScrollArea>
	</div>
</div>

<!-- <AlertDialog
	open={modalState.isDeleteOpen}
	customTitle={`This will delete Jail ${jail.name} (${jail.ctId})`}
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
				{`This will permanently delete Jail`}
				<span class="font-semibold">{jail?.name} ({jail?.ctId}).</span>
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
	logs={modalState.loading.title.toLowerCase().includes('deleting')
		? ''
		: startLogs?.logs || stopLogs?.logs || ''}
/>

<!-- <AlertDialog
	open={properties.resourceLimits.open}
	customTitle={jail.resourceLimits
		? 'This will give unlimited resources to this jail, proceed with <b>caution!</b>'
		: 'This will enable resource limits for this jail, defaulting to <b>1 GB RAM</b> and <b>1 vCPU</b>, you can change this later'}
	actions={{
		onConfirm: async () => {
			const response = await updateResourceLimits(jail.ctId, !jail.resourceLimits);
			if (response.error) {
				handleAPIError(response);
				let adjective = jail.resourceLimits ? 'disable' : 'enable';
				toast.error(`Failed to ${adjective} resource limits`, {
					position: 'bottom-center'
				});

				return;
			}

			let adjective = jail.resourceLimits ? 'disabled' : 'enabled';
			toast.success(`Resource limits ${adjective}`, {
				position: 'bottom-center'
			});
			properties.resourceLimits.open = false;
		},
		onCancel: () => {
			properties.resourceLimits.open = false;
		}
	}}
></AlertDialog> -->
