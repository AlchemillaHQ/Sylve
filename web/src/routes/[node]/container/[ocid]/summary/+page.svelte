<script lang="ts">
	import { getContainerLogs, getStats, updateContainerOptions } from '$lib/api/container/container';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import GuestInformation from '$lib/components/custom/Container/GuestInformation.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import type { GFSStep } from '$lib/types/common';
	import {
		containerTitle,
		type ContainerContext,
		type ContainerStatsBootstrap
	} from '$lib/types/container/container';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { getAPIErrorText, isAPIResponse } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import { Debounced, IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { getContext, untrack } from 'svelte';

	interface Data {
		node: string;
		ocId: number;
		stats: ContainerStatsBootstrap | null;
		ramInfo: RAMInfo;
		cpuInfo: CPUInfo;
	}

	let { data }: { data: Data } = $props();

	const container = getContext<ContainerContext>('container');
	const bar = getContext<{ step: GFSStep; showLogs: boolean }>('containerSummaryBar');
	let current = $derived(container.current);
	let running = $derived(current.runtime?.state === 'running');
	const visible = new IsDocumentVisible();

	const initial = untrack(() => data.stats);
	bar.step = initial?.resolvedStep ?? 'hourly';
	const stats = resource(
		() => bar.step,
		async (step, _, { signal }) => {
			const result = await getStats(data.ocId, step, { hostname: data.node, signal });
			if (isAPIResponse(result)) throw new Error(getAPIErrorText(result));
			return result;
		},
		{ lazy: true, initialValue: initial?.points ?? [] }
	);
	let emptyMessage = $derived(
		data.stats?.historyState === 'never-recorded' && stats.current.length === 0
			? 'No data has been recorded for this container yet.'
			: 'No samples exist in the selected time range.'
	);

	const logs = resource(
		() => bar.showLogs,
		async (open, _, { signal }) => {
			if (!open) return '';
			const result = await getContainerLogs(data.ocId, { hostname: data.node, signal });
			return isAPIResponse(result) ? getAPIErrorText(result) : result;
		},
		{ initialValue: '' }
	);

	useInterval(2000, {
		callback: () => {
			if (visible.current && running) container.refetch();
		}
	});
	useInterval(60000, {
		callback: () => {
			if (visible.current) stats.refetch();
		}
	});
	useInterval(3000, {
		callback: () => {
			if (visible.current && bar.showLogs) logs.refetch();
		}
	});

	let description = $state(untrack(() => container.current.description));
	const debouncedDescription = new Debounced(() => description, 500);
	async function saveDescription(value: string) {
		if (value === current.description) return;
		const result = await updateContainerOptions(data.ocId, { description: value }, data.node);
		if (result.status === 'error') toastContainerError(result);
	}
	watch(
		() => debouncedDescription.current,
		(value) => void saveDescription(value),
		{ lazy: true }
	);

	let since = $derived.by(() => {
		const runtime = current.runtime;
		if (!runtime) return '';
		const [label, at] = running ? ['Started', runtime.startedAt] : ['Stopped', runtime.finishedAt];
		return at.startsWith('0001') ? '' : `${label} ${dateToAgo(at)}`;
	});

	let renaming = $state({ editing: false, title: '', saving: false });
	async function rename() {
		const entered = renaming.title.trim();
		const title = entered === current.name ? '' : entered;
		if (title === current.title) return void (renaming.editing = false);
		renaming.saving = true;
		if (await container.setOptions({ title })) renaming.editing = false;
		renaming.saving = false;
	}
	const pendingLabels: Record<string, string> = {
		create: 'Creating',
		'create-stack': 'Creating Stack',
		upgrade: 'Updating Image',
		'upgrade-stack': 'Updating Stack',
		start: 'Starting',
		stop: 'Stopping',
		restart: 'Restarting',
		kill: 'Killing',
		update: 'Updating',
		delete: 'Deleting'
	};
	let status = $derived(
		pendingLabels[container.pendingAction] ??
			(current.creationPending
				? 'Creating'
				: running
					? 'Running'
					: current.runtime
						? 'Stopped'
						: 'Missing')
	);

	let cpuUsage = $derived(running ? current.cpuUsage : 0);
	let memoryUsage = $derived(running ? current.memoryUsage : 0);
	let cores = $derived(current.cores || data.cpuInfo?.logicalCores || 0);
	let memory = $derived(current.memory || data.ramInfo?.total || 0);

	const chartPoints = (key: 'cpuUsage' | 'memoryUsage') =>
		stats.current.map((point) => ({
			date: new Date(point.createdAt).getTime(),
			value: point[key]
		}));
</script>

<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
	<Card.Root class="w-full gap-0 p-4">
		<Card.Header class="p-0">
			<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
				<div class="group flex items-center gap-1.5">
					{#if renaming.editing}
						<!-- svelte-ignore a11y_autofocus -->
						<input
							class="w-40 min-w-0 border-b border-current bg-transparent outline-none"
							placeholder={current.name}
							bind:value={renaming.title}
							onkeydown={(e) => {
								if (e.key === 'Enter') rename();
								if (e.key === 'Escape') renaming.editing = false;
							}}
							autofocus
						/>
						<button
							onclick={rename}
							disabled={renaming.saving}
							class="text-current disabled:opacity-40"
							title="Save"
						>
							<span class="icon-[mdi--check] h-5 w-5"></span>
						</button>
						<button onclick={() => (renaming.editing = false)} class="text-current" title="Cancel">
							<span class="icon-[mdi--close] h-5 w-5"></span>
						</button>
					{:else}
						<span class="whitespace-nowrap">{containerTitle(current)}</span>
						{#if since}
							<span class="whitespace-nowrap">({since})</span>
						{/if}
						<button
							onclick={() =>
								(renaming = { editing: true, title: containerTitle(current), saving: false })}
							class="invisible text-current transition-opacity group-hover:visible"
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
				<div class="ml-auto">{status}</div>
			</div>

			<div class="mt-2">
				<div class="flex w-full justify-between pb-1">
					<p class="inline-flex items-center">
						<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>
						<span>CPU Usage</span>
					</p>
					<p class="ml-auto">{`${cpuUsage.toFixed(2)}% of ${cores} Core(s)`}</p>
				</div>
				<Progress value={cpuUsage} max={100} class="ml-auto h-2" />
			</div>

			<div class="mt-2">
				<div class="flex w-full justify-between pb-1">
					<p class="inline-flex items-center">
						<span class="icon-[ph--memory] mr-1 h-5 w-5"></span>
						<span>Memory Usage</span>
					</p>
					<p class="ml-auto">{`${memoryUsage.toFixed(2)}% of ${formatBytesBinary(memory)}`}</p>
				</div>
				<Progress value={memoryUsage} max={100} class="ml-auto h-2" />
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
				bind:value={description}
				textAreaClasses="!h-28"
				type="textarea"
			/>
		</Card.Content>
	</Card.Root>
</div>

<GuestInformation container={current} node={data.node} />

{#key bar.step}
	<div class="space-y-4 px-4 pb-4">
		<LineBrush
			title="CPU Usage"
			points={chartPoints('cpuUsage')}
			{emptyMessage}
			loading={stats.loading && stats.current.length === 0}
			error={Boolean(stats.error)}
			onRetry={() => stats.refetch()}
			percentage={true}
			color="one"
			animateOnMount={true}
			containerContentHeight="h-64"
			titleIconClass="icon-[solar--cpu-bold]"
		/>
		<LineBrush
			title="Memory Usage"
			points={chartPoints('memoryUsage')}
			{emptyMessage}
			loading={stats.loading && stats.current.length === 0}
			error={Boolean(stats.error)}
			onRetry={() => stats.refetch()}
			percentage={true}
			color="two"
			animateOnMount={true}
			containerContentHeight="h-64"
			titleIconClass="icon-[ph--memory]"
		/>
	</div>
{/key}

<Dialog.Root bind:open={bar.showLogs}>
	<Dialog.Content class="min-w-3xl overflow-hidden" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[material-symbols--terminal]"
					size="h-6 w-6"
					gap="gap-2"
					title={`${containerTitle(current)} Logs`}
				/>
			</Dialog.Title>
		</Dialog.Header>
		<div class="max-h-[60vh] overflow-auto rounded-md bg-black p-4">
			<pre class="whitespace-pre text-xs text-[#4AF626]">{logs.current ||
					(logs.loading ? 'Loading logs…' : 'No output.')}</pre>
		</div>
	</Dialog.Content>
</Dialog.Root>
