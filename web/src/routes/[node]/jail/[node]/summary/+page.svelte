<script lang="ts">
	import { goto } from '$app/navigation';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo } from '$lib/api/info/ram';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import {
		deleteJail,
		getJailById,
		getJailLogs,
		getJailStateById,
		getStats,
		jailAction,
		updateDescription
	} from '$lib/api/jail/jail';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { storage } from '$lib';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail, JailStat, JailState } from '$lib/types/jail/jail';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';
	import { resource, useInterval, IsDocumentVisible, Debounced, watch } from 'runed';
	import { untrack } from 'svelte';
	import type { GFSStep } from '$lib/types/common';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';

	interface Data {
		ctId: number;
		jail: Jail;
		state: JailState;
		stats: JailStat[];
		ramInfo: RAMInfo;
		cpuInfo: CPUInfo;
	}

	let visible = new IsDocumentVisible();

	let { data }: { data: Data } = $props();

	let ctId = $derived(data.ctId);
	let gfsStep = $state<GFSStep>('hourly');

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

	const jail = resource(
		() => `jail-${ctId}`,
		async (key, prevKey, { signal }) => {
			const result = await getJailById(ctId, 'ctid');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.jail
		}
	);

	const jState = resource(
		() => `jail-${ctId}-state`,
		async (key, prevKey, { signal }) => {
			const result = await getJailStateById(ctId);
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.state
		}
	);

	const logs = resource(
		() => `jail-${ctId}-logs`,
		async (key, prevKey, { signal }) => {
			const result = await getJailLogs(jail.current.ctId);
			updateCache(key, result);
			return result;
		},
		{
			initialValue: { logs: '' }
		}
	);

	const stats = resource(
		[() => gfsStep],
		async ([gfsStep]) => {
			const result = await getStats(Number(data.jail.ctId), gfsStep);
			const key = `jail-stats-${gfsStep}-${data.jail.ctId}`;
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.stats }
	);

	const cpuInfo = resource(
		() => `cpu-info`,
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('current');
			updateCache(key, result as CPUInfo);
			return result;
		},
		{
			initialValue: data.cpuInfo
		}
	);

	const ramInfo = resource(
		() => `ram-info`,
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.ramInfo
		}
	);

	let memoryUsage = $derived.by(() => {
		if (jState.current?.state === 'ACTIVE') {
			return (jState.current.memory / (jail.current.memory || ramInfo.current?.total || 1)) * 100;
		}
		return 0;
	});

	useInterval(1000, {
		callback: async () => {
			if (visible.current) {
				jail.refetch();
				jState.refetch();
				stats.refetch();
			}
		}
	});

	watch(
		() => visible.current,
		(isVisible) => {
			if (isVisible) {
				jail.refetch();
				jState.refetch();
				stats.refetch();
			}
		}
	);

	let showLogs = $state(false);
	let logicalCores = $derived(cpuInfo.current?.logicalCores ?? 0);
	let totalRAM = $derived(ramInfo.current?.total ?? 0);
	let jailDesc = $state(jail.current.description || '');
	let debouncedDesc = new Debounced(() => jailDesc, 500);
	let lastDesc = $state('');

	$effect(() => {
		const value = debouncedDesc.current;

		if (value !== undefined && value !== null && value !== lastDesc) {
			updateDescription(jail.current.id, value);
			lastDesc = value;
		}
	});

	let udTime = $derived.by(() => {
		if (jState.current.state === 'ACTIVE') {
			if (jail.current.startedAt) {
				return `Started ${dateToAgo(jail.current.startedAt)}`;
			}
		} else if (jState.current.state === 'INACTIVE' || jState.current.state === 'UNKNOWN') {
			if (jail.current.stoppedAt) {
				return `Stopped ${dateToAgo(jail.current.stoppedAt)}`;
			}
		}
		return '';
	});

	async function handleDelete() {
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being deleted`;
		modalState.loading.open = false;

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
			goto(`/${storage.hostname}/summary`);
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
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being stopped`;
		modalState.loading.iconColor = 'text-red-500';

		await sleep(1000);
		await jailAction(jail.current.ctId, 'stop');

		reload.leftPanel = true;
		modalState.loading.open = false;
	}

	async function handleStart() {
		modalState.loading.open = true;
		modalState.loading.title = 'Starting Jail';
		modalState.loading.description = `Please wait while Jail <b>${jail.current.name} (${jail.current.ctId})</b> is being started`;
		modalState.loading.iconColor = 'text-green-500';

		await sleep(1000);
		await jailAction(jail.current.ctId, 'start');

		reload.leftPanel = true;
		modalState.loading.open = false;
	}

	$effect(() => {
		if (showLogs) {
			untrack(() => {
				// scroll to the bottom of the logs
				const logsContainer = document.querySelector('.logs-container');
				if (logsContainer) {
					logsContainer.scrollTop = logsContainer.scrollHeight;
				}
			});
		}
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-4">
		{#if jState.current}
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
				<div class="flex items-center gap-2">
					<Button
						onclick={handleStart}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
						{'Start'}
					</Button>

					<Button
						onclick={() => {
							modalState.isDeleteOpen = true;
						}}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
						{'Delete'}
					</Button>
				</div>
			{/if}
		{/if}

		<div class="ml-auto flex h-full items-center gap-2">
			{#if logs.current.logs.length > 0}
				<Button
					size="sm"
					onclick={() => {
						showLogs = true;
					}}
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--file-document-outline] h-4 w-4"></span>
						<span>View Logs</span>
					</div>
				</Button>
			{/if}

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
							{`${jail.current?.name} ${udTime ? `(${udTime})` : ''}`}
						</Card.Description>
					</Card.Header>
					<Card.Content class="mt-3 p-0">
						<div class="flex items-start">
							<div class="flex items-center">
								<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
								{'Status'}
							</div>
							<div class="ml-auto">
								{jState.current?.state === 'ACTIVE'
									? 'Running'
									: jState.current?.state === 'INACTIVE'
										? 'Stopped'
										: jState.current?.state}
							</div>
						</div>

						<div class="mt-2">
							<div class="flex w-full justify-between pb-1">
								<p class="inline-flex items-center">
									<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>
									{'CPU Usage'}
								</p>
								<p class="ml-auto">
									{#if jState.current?.state === 'ACTIVE'}
										{`${jState.current.pcpu.toFixed(2)}% of ${jail.current.cores || logicalCores} Core(s)`}
									{:else}
										{`0% of ${jail.current.cores || logicalCores} Core(s)`}
									{/if}
								</p>
							</div>

							{#if jState.current?.state === 'ACTIVE'}
								<Progress value={jState.current.pcpu} max={100} class="ml-auto h-2" />
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
									{`${memoryUsage.toFixed(2)}% of ${humanFormat(jail.current.memory || totalRAM)}`}
								</p>
							</div>

							{#if jState.current?.state === 'ACTIVE'}
								<Progress
									value={(jState.current.memory / (jail.current.memory || totalRAM)) * 100}
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
				<LineBrush
					title="CPU Usage"
					points={stats.current.map((data) => ({
						date: new Date(data.createdAt).getTime(),
						value: Number(data.cpuUsage)
					}))}
					percentage={true}
					color="one"
					containerContentHeight="h-64"
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
				/>
			</div>
		</ScrollArea>
	</div>
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title>Are you sure?</AlertDialogRaw.Title>
			<AlertDialogRaw.Description>
				{`This will permanently delete Jail`}
				<span class="font-semibold">{jail.current.name} ({jail.current.ctId}).</span>
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
	logs={logs.current.logs}
/>

<Dialog.Root bind:open={showLogs}>
	<Dialog.Content
		class="min-w-3xl overflow-hidden"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<Dialog.Header class="flex w-full min-w-0 flex-col">
			<Dialog.Title class="flex justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--terminal] h-6 w-6"></span>
					<span>{jail.current.name} Logs</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							showLogs = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<Card.Root class="w-full min-w-0 gap-0 bg-black p-4 dark:bg-black">
			<Card.Content class="mt-3 w-full min-w-0 max-w-full p-0">
				<div class="logs-container max-h-64 w-full overflow-x-auto overflow-y-auto">
					<pre class="block min-w-0 whitespace-pre text-xs text-[#4AF626]">
							{logs.current.logs}
						</pre>
				</div>
			</Card.Content>
		</Card.Root>
	</Dialog.Content>
</Dialog.Root>

<style>
	.logs-container {
		overflow: auto; /* scrolling still works */
		scrollbar-width: none; /* Firefox */
		-ms-overflow-style: none; /* IE/Edge legacy */
	}

	/* Chrome, Safari, Edge Chromium */
	.logs-container::-webkit-scrollbar {
		display: none;
	}
</style>
