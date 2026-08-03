<script lang="ts">
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo } from '$lib/api/info/ram';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import {
		getJailById,
		getJailLogs,
		getStats,
		getStatsBootstrap,
		updateDescription,
		updateName
	} from '$lib/api/jail/jail';
	import * as Card from '$lib/components/ui/card/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail, JailState, JailStatsBootstrap } from '$lib/types/jail/jail';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { dateToAgo } from '$lib/utils/time';
	import { toast } from 'svelte-sonner';
	import { resource, useInterval, IsDocumentVisible, Debounced, watch } from 'runed';
	import { getContext, onMount } from 'svelte';
	import type { GFSStep } from '$lib/types/common';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface SummaryBarExtras {
		logsLength: number;
		showLogsCallback: () => void;
		gfsStep: GFSStep;
		refetchStats: (step?: GFSStep) => void;
		active: boolean;
	}

	interface Data {
		node: string;
		ctId: number;
		jail: Jail;
		stats: JailStatsBootstrap | null;
		ramInfo: RAMInfo;
		cpuInfo: CPUInfo;
	}

	let visible = new IsDocumentVisible();

	let { data }: { data: Data } = $props();

	let ctId = $derived(data.ctId);

	type JailStatsSnapshot = JailStatsBootstrap & {
		identity: string;
	};

	type StatsRefetchRequest = { step: GFSStep | null };

	function initialStatsSnapshot(): JailStatsSnapshot | null {
		const identity = `${data.node}:jail:${data.ctId}`;
		return data.stats
			? {
					identity,
					...data.stats
				}
			: null;
	}

	let statsIdentity = $derived(`${data.node}:jail:${data.ctId}`);
	let manualStatsSelection = $state<{ identity: string; step: GFSStep } | null>(null);
	let requestedStatsStep = $derived(
		manualStatsSelection?.identity === statsIdentity ? manualStatsSelection.step : null
	);

	function getStatsRefetchStep(refetching: unknown): GFSStep | null {
		if (typeof refetching !== 'object' || refetching === null || !('step' in refetching)) {
			return null;
		}

		return (refetching as StatsRefetchRequest).step;
	}

	function ensureCurrentStatsRequest(identity: string, signal: AbortSignal): void {
		if (signal.aborted || statsIdentity !== identity) {
			throw new DOMException('Obsolete jail statistics request', 'AbortError');
		}
	}

	const barExtras = getContext<SummaryBarExtras>('jailSummaryBarExtras');

	// svelte-ignore state_referenced_locally
	const jail = resource(
		[() => data.node, () => ctId],
		async ([hostname, currentCtID], _, { signal }) => {
			const result = await getJailById(currentCtID, 'ctid', { hostname, signal });
			updateCache(`jail-${currentCtID}`, result, hostname);
			return result;
		},
		{
			initialValue: data.jail
		}
	);

	const jState = getContext<{ current: JailState | null; refetch(): void }>('jailState');

	const logs = resource(
		[() => data.node, () => ctId],
		async ([hostname, currentCtID], _, { signal }) => {
			const result = await getJailLogs(currentCtID, { hostname, signal });
			updateCache(`jail-${currentCtID}-logs`, result, hostname);
			return result;
		},
		{
			initialValue: { logs: '' }
		}
	);

	let lastAutomaticStatsResolution = 0;

	const stats = resource(
		[() => data.node, () => ctId],
		async (
			[hostname, currentCtID],
			_,
			{ data: previousSnapshot, refetching, signal }
		): Promise<JailStatsSnapshot | null> => {
			const identity = `${hostname}:jail:${currentCtID}`;
			const requestedStep = getStatsRefetchStep(refetching);

			if (!requestedStep) {
				const result = await getStatsBootstrap(currentCtID, { hostname, signal });
				if (isAPIResponse(result)) {
					throw new Error(
						result.message || result.error?.toString() || 'Failed to load jail statistics'
					);
				}

				ensureCurrentStatsRequest(identity, signal);

				const snapshot: JailStatsSnapshot = {
					identity,
					...result
				};
				await updateCache(`guest-stats-v2:jail:${currentCtID}:bootstrap`, result, hostname);
				ensureCurrentStatsRequest(identity, signal);
				lastAutomaticStatsResolution = Date.now();
				return snapshot;
			}

			const result = await getStats(currentCtID, requestedStep, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(
					result.message || result.error?.toString() || 'Failed to load jail statistics'
				);
			}
			ensureCurrentStatsRequest(identity, signal);

			const previous = previousSnapshot?.identity === identity ? previousSnapshot : null;
			const snapshot: JailStatsSnapshot = {
				identity,
				points: result,
				resolvedStep: requestedStep,
				lastSampleAt: result.at(-1)?.createdAt ?? previous?.lastSampleAt ?? null,
				historyState: result.length > 0 ? 'available' : (previous?.historyState ?? 'available')
			};
			return snapshot;
		},
		{ initialValue: initialStatsSnapshot() }
	);

	let activeStats = $derived.by(() =>
		stats.current?.identity === statsIdentity ? stats.current : null
	);
	let statsPoints = $derived(activeStats?.points ?? []);
	let displayedStatsStep = $derived(activeStats?.resolvedStep ?? null);
	let statsEmptyMessage = $derived.by(() => {
		if (!activeStats) {
			return stats.error ? 'Unable to load telemetry. Retrying…' : 'Loading telemetry…';
		}
		if (
			requestedStatsStep &&
			displayedStatsStep === requestedStatsStep &&
			statsPoints.length === 0
		) {
			return 'No samples exist in the selected time range.';
		}
		if (activeStats.historyState === 'never-recorded') {
			return 'No data has been recorded for this jail.';
		}
		if (activeStats.historyState === 'outside-supported-range') {
			return activeStats.lastSampleAt
				? `Last telemetry was recorded ${dateToAgo(activeStats.lastSampleAt)}; graph history is available for 70 days.`
				: 'The last telemetry is outside the supported 70-day graph range.';
		}
		if (statsPoints.length === 0) return 'No samples exist in the selected time range.';
		return '';
	});

	watch(
		() => displayedStatsStep,
		(step) => {
			if (step) barExtras.gfsStep = step;
		}
	);

	function refetchCurrentStats() {
		return stats.refetch({ step: requestedStatsStep ?? displayedStatsStep });
	}

	function selectStatsStep(step: GFSStep) {
		manualStatsSelection = { identity: statsIdentity, step };
		if (displayedStatsStep) barExtras.gfsStep = displayedStatsStep;
		void stats.refetch({ step });
	}

	// svelte-ignore state_referenced_locally
	const cpuInfo = resource(
		() => data.node,
		async (hostname, _, { signal }) => {
			const result = await getCPUInfo('current', { hostname, signal });
			updateCache('system-cpu-info', result as CPUInfo, hostname);
			return result;
		},
		{
			initialValue: data.cpuInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const ramInfo = resource(
		() => data.node,
		async (hostname, _, { signal }) => {
			const result = await getRAMInfo('current', { hostname, signal });
			updateCache('system-ram-info', result, hostname);
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

				const pollingStep = requestedStatsStep ?? displayedStatsStep;
				const shouldReResolve =
					requestedStatsStep === null &&
					jState.current?.state === 'ACTIVE' &&
					Date.now() - lastAutomaticStatsResolution >= 60000;
				if (!stats.loading && (stats.error || pollingStep === 'hourly' || shouldReResolve)) {
					if (shouldReResolve) void stats.refetch({ step: null });
					else void refetchCurrentStats();
				}
			}
		}
	});

	useInterval(3000, {
		callback: () => {
			if (visible.current && showLogs) {
				logs.refetch();
			}
		}
	});

	watch(
		() => jState.current?.state,
		(currentState, previousState) => {
			if (previousState && previousState !== currentState && requestedStatsStep === null) {
				void stats.refetch({ step: null });
			}
		}
	);

	watch(
		() => visible.current,
		(isVisible) => {
			if (isVisible) {
				jail.refetch();
				if (!stats.loading) void refetchCurrentStats();

				if (showLogs) {
					logs.refetch();
				}
			}
		}
	);

	let showLogs = $state(false);
	let logsContainerElement = $state<HTMLDivElement | null>(null);
	let followLogs = $state(true);
	const LOG_AUTO_SCROLL_THRESHOLD = 24;
	let logicalCores = $derived(cpuInfo.current?.logicalCores ?? 0);
	let totalRAM = $derived(ramInfo.current?.total ?? 0);
	let jailDesc = $state(jail.current.description || '');
	let debouncedDesc = new Debounced(() => jailDesc, 500);
	let isDescInitialized = false;
	let jailName = $state(jail.current.name || '');
	let syncedJailName = $state(jail.current.name || '');
	let isRenameInFlight = $state(false);
	let isEditingName = $state(false);

	function startEditingName() {
		isEditingName = true;
	}

	function cancelEditingName() {
		jailName = syncedJailName;
		isEditingName = false;
	}

	function isNearLogsBottom(element: HTMLDivElement): boolean {
		return (
			element.scrollHeight - element.scrollTop - element.clientHeight <= LOG_AUTO_SCROLL_THRESHOLD
		);
	}

	function handleLogsScroll() {
		if (!logsContainerElement) {
			return;
		}

		followLogs = isNearLogsBottom(logsContainerElement);
	}

	watch(
		() => debouncedDesc.current,
		(curr, prev) => {
			if (!isDescInitialized) {
				isDescInitialized = true;
				return;
			}

			if (curr !== undefined && prev !== undefined) {
				if (curr !== prev) {
					updateDescription(jail.current.id, curr);
				}
			}
		}
	);

	watch(
		() => jail.current.name,
		(currentName) => {
			const normalized = currentName || '';
			if (!isRenameInFlight && jailName === syncedJailName) {
				jailName = normalized;
			}
			syncedJailName = normalized;
		}
	);

	let udTime = $derived.by(() => {
		if (jState.current?.state === 'ACTIVE') {
			if (jail.current.startedAt) {
				return `Started ${dateToAgo(jail.current.startedAt)}`;
			}
		} else if (jState.current?.state === 'INACTIVE' || jState.current?.state === 'UNKNOWN') {
			if (jail.current.stoppedAt) {
				return `Stopped ${dateToAgo(jail.current.stoppedAt)}`;
			}
		}
		return '';
	});

	let isJailNameDirty = $derived.by(
		() => jailName.trim() !== String(jail.current.name || '').trim()
	);

	let canSaveJailName = $derived.by(
		() => !isRenameInFlight && jailName.trim() !== '' && isJailNameDirty
	);

	async function handleRename() {
		const nextName = jailName.trim();
		const currentName = String(jail.current.name || '').trim();

		if (!nextName || nextName === currentName || isRenameInFlight) {
			return;
		}

		isRenameInFlight = true;
		const result = await updateName(jail.current.id, nextName);
		if (result.status === 'success') {
			isEditingName = false;
			reload.leftPanel = true;
			await jail.refetch();
			jailName = jail.current.name || nextName;
			syncedJailName = jailName;
			toast.success('Jail name updated', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else {
			let errorMessage = 'Error updating jail name';
			if (result.message === 'invalid_vm_name') {
				errorMessage = 'Invalid jail name. Use letters, numbers, - or _.';
			} else if (result.message === 'jail_name_already_in_use') {
				errorMessage = 'Jail name is already in use';
			} else if (result.message === 'replication_lease_not_owned') {
				errorMessage = 'This jail is owned by another node right now';
			}

			toast.error(errorMessage, {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		isRenameInFlight = false;
	}

	// Register with the layout's toolbar bar on mount; clean up on unmount
	onMount(() => {
		barExtras.showLogsCallback = () => {
			followLogs = true;
			showLogs = true;
		};
		barExtras.refetchStats = (step) => {
			if (step) {
				selectStatsStep(step);
				return;
			}
			void refetchCurrentStats();
		};
		return () => {
			barExtras.logsLength = 0;
			barExtras.refetchStats = () => {};
		};
	});

	// Keep logsLength in the shared bar state in sync
	watch(
		() => logs.current.logs.length,
		(length) => {
			barExtras.logsLength = length;
		}
	);

	watch(
		() => logs.current.logs,
		() => {
			if (showLogs && followLogs) {
				requestAnimationFrame(() => {
					if (logsContainerElement) {
						logsContainerElement.scrollTop = logsContainerElement.scrollHeight;
					}
				});
			}
		}
	);

	watch(
		() => showLogs,
		(isOpen) => {
			if (isOpen) {
				followLogs = true;
				requestAnimationFrame(() => {
					if (logsContainerElement) {
						logsContainerElement.scrollTop = logsContainerElement.scrollHeight;
					}
				});
			}
		}
	);
</script>

<div>
	<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
		<Card.Root class="w-full gap-0 p-4">
			<Card.Header class="p-0">
				<Card.Description class="text-md  font-normal text-blue-600 dark:text-blue-500">
					<div class="group flex items-center gap-1.5">
						{#if isEditingName}
							<!-- svelte-ignore a11y_autofocus -->
							<input
								class="border-b border-current bg-transparent outline-none min-w-0 w-40"
								bind:value={jailName}
								onkeydown={(e) => {
									if (e.key === 'Enter' && canSaveJailName) handleRename();
									if (e.key === 'Escape') cancelEditingName();
								}}
								autofocus
							/>
							<button
								onclick={handleRename}
								disabled={!canSaveJailName || isRenameInFlight}
								class="text-current disabled:opacity-40"
								title="Save"
							>
								<span class="icon-[mdi--check] h-5 w-5"></span>
							</button>
							<button onclick={cancelEditingName} class="text-current" title="Cancel">
								<span class="icon-[mdi--close] h-5 w-5"></span>
							</button>
						{:else}
							<span class="whitespace-nowrap">{jail.current.name}</span>
							{#if udTime}
								<span class="whitespace-nowrap">({udTime})</span>
							{/if}
							<button
								onclick={startEditingName}
								class="invisible group-hover:visible transition-opacity text-current"
								title="Edit Name"
							>
								<span class="icon-[mdi--pencil] h-3.5 w-3.5"></span>
							</button>
						{/if}
					</div>
				</Card.Description>
			</Card.Header>
			<Card.Content class="mt-3 p-0">
				<div class="flex items-start">
					<div class="flex items-center">
						<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
						<span>Status</span>
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
							<span>CPU Usage</span>
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
							<span>Memory Usage</span>
						</p>
						<p class="ml-auto">
							{`${memoryUsage.toFixed(2)}% of ${formatBytesBinary(jail.current.memory || totalRAM)}`}
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
					placeholder="Notes"
					bind:value={jailDesc}
					classes=""
					textAreaClasses="!h-28"
					type="textarea"
				/>
			</Card.Content>
		</Card.Root>
	</div>

	{#key statsIdentity}
		<div class="space-y-4 px-4 pb-4">
			<LineBrush
				title="CPU Usage"
				points={statsPoints.map((data) => ({
					date: new Date(data.createdAt).getTime(),
					value: Number(data.cpuUsage)
				}))}
				emptyMessage={statsEmptyMessage}
				loading={stats.loading && !activeStats}
				error={Boolean(stats.error)}
				onRetry={() => void refetchCurrentStats()}
				percentage={true}
				color="one"
				containerContentHeight="h-64"
				titleIconClass="icon-[solar--cpu-bold]"
			/>

			<LineBrush
				title="Memory Usage"
				points={statsPoints.map((data) => ({
					date: new Date(data.createdAt).getTime(),
					value: Number(data.memoryUsage)
				}))}
				emptyMessage={statsEmptyMessage}
				loading={stats.loading && !activeStats}
				error={Boolean(stats.error)}
				onRetry={() => void refetchCurrentStats()}
				percentage={true}
				color="two"
				containerContentHeight="h-64"
				titleIconClass="icon-[ph--memory]"
			/>
		</div>
	{/key}
</div>

<Dialog.Root bind:open={showLogs}>
	<Dialog.Content
		class="min-w-3xl overflow-hidden"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		showCloseButton={true}
		onClose={() => {
			showLogs = false;
		}}
	>
		<Dialog.Header class="flex w-full min-w-0 flex-col">
			<Dialog.Title class="flex justify-between text-left">
				<SpanWithIcon
					icon="icon-[material-symbols--terminal]"
					size="h-6 w-6"
					gap="gap-2"
					title={`${jail.current.name} Logs`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<Card.Root class="w-full min-w-0 gap-0 bg-black p-4 dark:bg-black">
			<Card.Content class="mt-3 w-full min-w-0 max-w-full p-0">
				<div
					class="logs-container max-h-64 w-full overflow-x-auto overflow-y-auto"
					bind:this={logsContainerElement}
					onscroll={handleLogsScroll}
				>
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
		overflow: auto;
		scrollbar-width: none;
		-ms-overflow-style: none;
	}

	.logs-container::-webkit-scrollbar {
		display: none;
	}
</style>
