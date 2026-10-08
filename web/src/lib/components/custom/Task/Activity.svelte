<script lang="ts">
	import { storage } from '$lib';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getSimpleJails, getSimpleJailTemplates } from '$lib/api/jail/jail';
	import { getActiveLifecycleTasks, getRecentLifecycleTasks } from '$lib/api/task/lifecycle';
	import { getSimpleVMs, getSimpleVMTemplates } from '$lib/api/vm/vm';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { APIResponse } from '$lib/types/common';
	import {
		isLifecycleTaskActive,
		type ActiveLifecycleGuest,
		type LifecycleTask
	} from '$lib/types/task/lifecycle';
	import { getAPIErrorText, isAPIResponse } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { prefersReducedMotion } from 'svelte/motion';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { fade } from 'svelte/transition';

	interface Props {
		hostname: string;
		clustered?: boolean;
		onActiveGuestsChange?: (activeGuests: ActiveLifecycleGuest[]) => void;
	}

	type LifecycleGuestNames = Partial<Record<LifecycleTask['guestType'], Map<number, string>>>;

	let { hostname, clustered = false, onActiveGuestsChange }: Props = $props();
	let open = $state(false);
	let selectedHostname = $state('');
	let tasksLoading = $state(false);
	let taskRequestHostname = $state('');
	let manualRefreshing = $state(false);
	let minimumLoading = $state(false);
	let sortAscending = $state(false);
	let refreshQueued = false;
	const effectiveHostname = $derived((clustered && selectedHostname) || hostname);

	const nodes = resource(
		() => clustered,
		async (enabled, _previous, { signal }) => (enabled ? await getNodes(signal) : []),
		{ initialValue: [] as ClusterNode[] }
	);

	const hostnameOptions = $derived.by(() => {
		const names = new SvelteSet<string>();
		if (hostname) names.add(hostname);
		if (effectiveHostname) names.add(effectiveHostname);
		for (const node of nodes.current) {
			if (node.hostname) names.add(node.hostname);
		}
		return Array.from(names)
			.sort((a, b) => a.localeCompare(b))
			.map((name) => ({ value: name, label: name }));
	});

	const taskActivity = resource(
		() => [effectiveHostname, open] as const,
		async ([node, includeRecent], _previous, { signal }) => {
			taskRequestHostname = node;
			tasksLoading = true;
			try {
				const [active, recent] = await Promise.all([
					node ? getActiveLifecycleTasks(undefined, undefined, node, signal) : [],
					node && includeRecent
						? getRecentLifecycleTasks(50, undefined, undefined, node, signal)
						: null
				]);
				if (signal.aborted) throw new DOMException('Request aborted', 'AbortError');
				return { hostname: node, active, recent };
			} finally {
				if (!signal.aborted) tasksLoading = false;
			}
		},
		{
			initialValue: {
				hostname: '',
				active: [] as LifecycleTask[] | APIResponse,
				recent: null as LifecycleTask[] | APIResponse | null
			}
		}
	);

	const guestNames = resource(
		() => (open ? effectiveHostname : ''),
		async (node, _previous, { signal }) => {
			if (!node) return { hostname: node, names: {} as LifecycleGuestNames };
			const [vms, jails, jailTemplates, vmTemplates] = await Promise.all([
				getSimpleVMs(node, signal).catch(() => []),
				getSimpleJails(node, signal).catch(() => []),
				getSimpleJailTemplates(node).catch(() => []),
				getSimpleVMTemplates(node).catch(() => [])
			]);
			if (signal.aborted) throw new DOMException('Request aborted', 'AbortError');
			return {
				hostname: node,
				names: {
					vm: new Map(vms.map((vm) => [vm.rid, vm.name])),
					jail: new Map(jails.map((jail) => [jail.ctId, jail.name])),
					'jail-template': new Map(jailTemplates.map((template) => [template.id, template.name])),
					'vm-template': new Map(vmTemplates.map((template) => [template.id, template.name]))
				}
			};
		},
		{ initialValue: { hostname: '', names: {} as LifecycleGuestNames } }
	);

	const activeResult = $derived(
		taskActivity.current.hostname === effectiveHostname ? taskActivity.current.active : null
	);
	const recentResult = $derived(
		taskActivity.current.hostname === effectiveHostname ? taskActivity.current.recent : null
	);
	const taskList = $derived.by(() => {
		const tasks = new SvelteMap<number, LifecycleTask>();
		for (const task of [
			...(Array.isArray(activeResult) ? activeResult : []),
			...(Array.isArray(recentResult) ? recentResult : [])
		]) {
			const previous = tasks.get(task.id);
			if (
				!previous ||
				Date.parse(task.updatedAt) > Date.parse(previous.updatedAt) ||
				(Date.parse(task.updatedAt) === Date.parse(previous.updatedAt) &&
					!isLifecycleTaskActive(task))
			) {
				tasks.set(task.id, task);
			}
		}
		return Array.from(tasks.values());
	});
	const activeTaskList = $derived(taskList.filter(isLifecycleTaskActive));
	const activeCount = $derived(activeTaskList.length);
	const taskRequestFailed = $derived(
		taskRequestHostname === effectiveHostname && !tasksLoading && !!taskActivity.error
	);
	const activeError = $derived(
		isAPIResponse(activeResult)
			? getAPIErrorText(activeResult)
			: taskRequestFailed
				? 'Failed to load active tasks.'
				: ''
	);
	const recentError = $derived(isAPIResponse(recentResult) ? getAPIErrorText(recentResult) : '');
	const names = $derived(
		guestNames.current.hostname === effectiveHostname ? guestNames.current.names : {}
	);
	const activeGuests = $derived(
		activeTaskList.map((task) => ({
			hostname: effectiveHostname,
			guestType: task.guestType,
			guestId: task.guestId
		}))
	);
	const modalLoading = $derived(
		(recentResult === null && !taskRequestFailed) ||
			(!activeError && !recentError && guestNames.current.hostname !== effectiveHostname)
	);
	let modalView = $state.raw({
		hostname: '',
		tasks: [] as LifecycleTask[],
		names: {} as LifecycleGuestNames,
		error: '',
		loading: true,
		nodesError: false
	});
	const showLoader = $derived(
		open && (minimumLoading || modalLoading || modalView.hostname !== effectiveHostname)
	);
	const sortLabel = $derived(
		sortAscending ? 'Execution order: oldest first' : 'Execution order: newest first'
	);

	watch.pre(
		() => [open, effectiveHostname] as const,
		([isOpen]) => {
			minimumLoading = isOpen;
			if (!isOpen) return;
			const timeout = setTimeout(() => (minimumLoading = false), 1000);
			return () => clearTimeout(timeout);
		}
	);

	watch.pre(
		() =>
			open
				? {
						hostname: effectiveHostname,
						tasks: taskList,
						names,
						error: activeError || recentError,
						loading: minimumLoading || modalLoading,
						nodesError: !!nodes.error
					}
				: null,
		(view) => {
			if (view && (!view.loading || !modalView.hostname)) modalView = view;
		}
	);

	watch(
		() => activeGuests,
		(guests) => onActiveGuestsChange?.(guests)
	);
	watch(
		() => hostname,
		() => {
			selectedHostname = '';
		}
	);
	watch(
		() => tasksLoading,
		(loading) => {
			if (!loading && refreshQueued) refreshTasks();
		}
	);
	watch(
		() => reload.lifecycleTasksPulse,
		() => {
			const node = reload.lifecycleTasksHostname || hostname;
			if (clustered && !open && node && node !== effectiveHostname) {
				selectedHostname = node;
				return;
			}
			refreshTasks(true);
			if (open && !guestNames.loading) guestNames.refetch();
		}
	);
	watch(
		() => reload.datacenterNodesPulse,
		() => {
			if (!storage.visible) return;
			refreshTasks(true);
			if (clustered && !nodes.loading) nodes.refetch();
			if (open && !guestNames.loading) guestNames.refetch();
		}
	);

	watch(
		() => storage.visible,
		(visible) => {
			if (visible) refreshTasks(true);
		}
	);

	function refreshTasks(urgent = false) {
		if (tasksLoading) {
			if (urgent) refreshQueued = true;
			return;
		}
		refreshQueued = false;
		taskActivity.refetch();
	}

	async function refreshManually() {
		if (manualRefreshing) return;
		manualRefreshing = true;
		try {
			await Promise.all([
				taskActivity.refetch(),
				!guestNames.loading ? guestNames.refetch() : undefined,
				clustered && !nodes.loading ? nodes.refetch() : undefined
			]);
		} finally {
			manualRefreshing = false;
		}
	}

	useInterval(1000, {
		callback: () => {
			if (storage.visible) refreshTasks();
		}
	});

	function actionLabel(action: string): string {
		return (
			action
				.replace(/[_-]+/g, ' ')
				.trim()
				.split(/\s+/)
				.map((word) => word.charAt(0).toUpperCase() + word.slice(1))
				.join(' ') || 'Working'
		);
	}

	function lifecycleStatusMeta(status: LifecycleTask['status']) {
		switch (status) {
			case 'queued':
				return {
					label: 'Queued',
					icon: 'icon-[mdi--clock-outline]',
					className: 'border-yellow-500/50 bg-yellow-500/15 text-yellow-700 dark:text-yellow-400'
				};
			case 'running':
				return {
					label: 'Running',
					icon: 'icon-[mdi--loading] animate-spin motion-reduce:animate-none',
					className: 'border-blue-500/50 bg-blue-500/15 text-blue-700 dark:text-blue-400'
				};
			case 'success':
				return {
					label: 'Success',
					icon: 'icon-[mdi--check-circle-outline]',
					className: 'border-green-500/50 bg-green-500/15 text-green-700 dark:text-green-400'
				};
			case 'failed':
				return {
					label: 'Failed',
					icon: 'icon-[mdi--alert-circle-outline]',
					className: 'border-destructive/50 bg-destructive/15 text-destructive'
				};
		}
	}

	const compactBadgeClass = 'rounded px-1 py-0 font-mono text-xs leading-tight';
	const timeBadgeClass = `${compactBadgeClass} border-muted-foreground/30 text-muted-foreground`;

	function lifecycleActionLabel(task: LifecycleTask): string {
		if (task.action === 'convert') return 'Create Template';
		if (task.action === 'create' && task.guestType.endsWith('-template')) {
			return 'Create from Template';
		}
		return actionLabel(task.action);
	}

	function lifecycleActionIcon(action: string): string {
		switch (action) {
			case 'start':
				return 'icon-[mdi--play]';
			case 'stop':
				return 'icon-[mdi--stop]';
			case 'shutdown':
				return 'icon-[mdi--power]';
			case 'reboot':
			case 'restart':
				return 'icon-[mdi--restart]';
			case 'create':
				return 'icon-[mdi--plus-circle-outline]';
			case 'convert':
				return 'icon-[mdi--content-copy]';
			case 'migrate':
				return 'icon-[mdi--swap-horizontal]';
			default:
				return 'icon-[mdi--cog-outline]';
		}
	}

	function lifecycleGuestTypeLabel(guestType: LifecycleTask['guestType']): string {
		switch (guestType) {
			case 'vm':
				return 'VM';
			case 'jail':
				return 'Jail';
			case 'vm-template':
				return 'VM Template';
			case 'jail-template':
				return 'Jail Template';
		}
	}

	function lifecycleGuestLabel(task: LifecycleTask, names: LifecycleGuestNames): string {
		const guestType =
			task.action === 'convert'
				? task.guestType === 'vm-template'
					? 'vm'
					: 'jail'
				: task.guestType;
		let name = names[guestType]?.get(task.guestId) || '';
		if (task.guestType === 'jail' && task.action === 'create') {
			try {
				const request = JSON.parse(task.payload || '{}');
				if (typeof request?.name === 'string') name = request.name;
			} catch {
				// Older task payloads may not include creation settings.
			}
		}
		return name ? `${name} (${task.guestId})` : `#${task.guestId}`;
	}

	function taskTime(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		return date.toLocaleTimeString(undefined, {
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit',
			hour12: false
		});
	}

	function sortTasks(tasks: LifecycleTask[]): LifecycleTask[] {
		const direction = sortAscending ? 1 : -1;
		return [...tasks].sort(
			(a, b) =>
				direction *
				(Date.parse(a.startedAt || a.createdAt) - Date.parse(b.startedAt || b.createdAt) ||
					a.id - b.id)
		);
	}
</script>

<Button
	size="sm"
	variant="outline"
	class="h-6 shrink-0 gap-2"
	title={activeError ? 'Task status unavailable' : `Tasks on ${effectiveHostname}`}
	aria-label={activeError
		? 'Open tasks; task status unavailable'
		: `Open tasks (${activeCount} active on ${effectiveHostname})`}
	aria-haspopup="dialog"
	aria-expanded={open}
	onclick={() => (open = true)}
>
	<span
		class={activeError
			? 'icon-[mdi--alert-circle-outline] text-destructive h-4 w-4'
			: activeCount > 0
				? 'icon-[mdi--loading] h-4 w-4 animate-spin motion-reduce:animate-none'
				: 'icon-[mdi--progress-clock] h-4 w-4'}
	></span>
	<span class="hidden sm:inline">Tasks</span>
	{#if activeCount > 0}
		<span
			class="bg-primary text-primary-foreground inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full px-1 text-[10px]"
		>
			{activeCount > 99 ? '99+' : activeCount}
		</span>
	{/if}
</Button>

{#snippet timeBadge(label: string, icon: string, value: string)}
	<Badge variant="outline" class={timeBadgeClass} title={`${label}: ${convertDbTime(value)}`}>
		<span class={`h-3 w-3 shrink-0 ${icon}`} aria-hidden="true"></span>
		<span class="sr-only">{label}</span>
		<time datetime={value}>{taskTime(value)}</time>
	</Badge>
{/snippet}

{#snippet taskCard(task: LifecycleTask, node: string, names: LifecycleGuestNames)}
	{@const status = lifecycleStatusMeta(task.status)}
	{@const message = task.message?.trim() || ''}
	{@const stateMessage = message && /^[a-z][a-z0-9_-]*$/i.test(message)}
	<div class="space-y-1.5 rounded-md border px-2.5 py-2">
		<div class="flex items-start justify-between gap-2">
			<div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs font-medium">
				<span class="inline-flex shrink-0 items-center gap-1">
					<span class={`h-3.5 w-3.5 ${lifecycleActionIcon(task.action)}`} aria-hidden="true"></span>
					{lifecycleActionLabel(task)}
				</span>
				<span class="text-muted-foreground" aria-hidden="true">·</span>
				<span class="inline-flex min-w-0 items-center gap-1">
					<span
						class={`h-3.5 w-3.5 shrink-0 ${
							task.guestType.startsWith('vm')
								? 'icon-[material-symbols--monitor-outline]'
								: 'icon-[hugeicons--prison]'
						}`}
						aria-hidden="true"
					></span>
					<span class="sr-only">{lifecycleGuestTypeLabel(task.guestType)}</span>
					<span class="break-words">{lifecycleGuestLabel(task, names)}</span>
				</span>
			</div>
			<div class="flex shrink-0 flex-wrap items-center justify-end gap-1">
				<Badge variant="outline" class={`${compactBadgeClass} ${status.className}`}>
					<span class={`h-3 w-3 ${status.icon}`} aria-hidden="true"></span>
					{status.label}
				</Badge>
				{#if stateMessage && message.toLowerCase() !== task.status}
					<Badge variant="outline" class={`${compactBadgeClass} ${status.className}`}>
						<span
							class={`h-3 w-3 ${
								message.toLowerCase() === 'completed' ? 'icon-[mdi--check-all]' : status.icon
							}`}
							aria-hidden="true"
						></span>
						{actionLabel(message)}
					</Badge>
				{/if}
			</div>
		</div>
		<div
			class="text-muted-foreground flex items-center gap-2 overflow-x-auto whitespace-nowrap text-xs"
		>
			<span class="inline-flex shrink-0 items-center gap-1" title="Node">
				<span class="icon-[mdi--server] h-3 w-3 shrink-0"></span>
				<span>{node}</span>
			</span>
			<span class="shrink-0" aria-hidden="true">·</span>
			<span class="inline-flex shrink-0 items-center gap-1" title="Requested By">
				<span class="icon-[mdi--account-outline] h-3 w-3 shrink-0"></span>
				<span>{task.requestedBy || task.source || '-'}</span>
			</span>
			<div class="ml-auto flex shrink-0 items-center gap-1">
				{@render timeBadge('Created', 'icon-[mdi--calendar-plus]', task.createdAt)}
				{#if task.startedAt}
					{@render timeBadge('Started', 'icon-[mdi--play-circle-outline]', task.startedAt)}
				{/if}
				{#if task.finishedAt}
					{@render timeBadge('Finished', 'icon-[mdi--flag-checkered]', task.finishedAt)}
				{/if}
			</div>
		</div>
		{#if message && !stateMessage}
			<p class="text-muted-foreground text-xs whitespace-pre-wrap break-words">{message}</p>
		{/if}
		{#if task.error}
			<p class="text-destructive text-xs whitespace-pre-wrap break-words">{task.error}</p>
		{/if}
	</div>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[min(32rem,85vh)] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[35rem]"
		showCloseButton={false}
		aria-describedby={undefined}
	>
		<Dialog.Header class="shrink-0 border-b px-3 py-3 text-left">
			<div class="flex items-center justify-between gap-2">
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--progress-clock]"
						title="Recent Tasks"
						size="h-5 w-5"
						gap="gap-2"
					/>
				</Dialog.Title>
				<Dialog.Close
					class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-sm outline-none focus-visible:ring-2"
				>
					<span class="icon-[lucide--x] h-5 w-5" aria-hidden="true"></span>
					<span class="sr-only">Close</span>
				</Dialog.Close>
			</div>
		</Dialog.Header>

		<div class="flex shrink-0 items-center justify-between gap-2 border-b px-3 py-2">
			<SimpleSelect
				placeholder="Node"
				options={hostnameOptions}
				value={effectiveHostname}
				size="sm"
				disabled={!clustered}
				onChange={(value) => (selectedHostname = value)}
				classes={{
					parent: 'w-48 min-w-0 space-y-0',
					trigger: 'h-8 w-full text-xs'
				}}
			/>
			<div class="flex shrink-0 items-center gap-1.5">
				<Button
					size="icon"
					variant="outline"
					class="size-8"
					title={sortLabel}
					aria-label={sortLabel}
					aria-pressed={sortAscending}
					onclick={() => (sortAscending = !sortAscending)}
				>
					<span
						class={`h-4 w-4 ${
							sortAscending
								? 'icon-[mdi--sort-clock-ascending-outline]'
								: 'icon-[mdi--sort-clock-descending-outline]'
						}`}
						aria-hidden="true"
					></span>
				</Button>
				<Button
					size="icon"
					variant="outline"
					class="size-8"
					title="Refresh"
					aria-label="Refresh"
					disabled={manualRefreshing}
					onclick={refreshManually}
				>
					<span
						class="icon-[mdi--refresh] h-4 w-4 motion-reduce:animate-none"
						class:animate-spin={manualRefreshing}
						aria-hidden="true"
					></span>
				</Button>
			</div>
		</div>

		<div class="relative min-h-0 flex-1" aria-busy={showLoader}>
			{#if open}
				{#key `${modalView.hostname}:${modalView.loading}`}
					{@const view = modalView}
					{@const sortedTasks = sortTasks(view.tasks)}
					<div
						class="absolute inset-0 overflow-y-auto overscroll-contain p-3"
						in:fade|global={{ duration: prefersReducedMotion.current ? 0 : 180 }}
						out:fade|global={{ duration: prefersReducedMotion.current ? 0 : 120 }}
					>
						<div
							class="space-y-2 transition-opacity duration-150 motion-reduce:transition-none"
							class:opacity-50={showLoader}
						>
							{#if clustered && view.nodesError}
								<p class="text-destructive text-sm">Failed to load cluster nodes.</p>
							{/if}
							{#if view.error}
								<p
									class="bg-destructive/10 text-destructive rounded-md border border-destructive/30 p-2 text-xs"
									role="alert"
								>
									{view.error}
								</p>
							{/if}
							{#if view.tasks.length > 0}
								{#each sortedTasks as task (task.id)}
									{@render taskCard(task, view.hostname, view.names)}
								{/each}
							{:else if !view.error && !view.loading}
								<p class="bg-muted/20 text-muted-foreground rounded-md border p-2 text-xs">
									No recent tasks on this node.
								</p>
							{/if}
						</div>
					</div>
				{/key}
			{/if}
			{#if showLoader}
				<div
					class="pointer-events-none absolute inset-0 flex items-center justify-center"
					role="status"
					in:fade|global={{ duration: prefersReducedMotion.current ? 0 : 180 }}
					out:fade|global={{ duration: prefersReducedMotion.current ? 0 : 120 }}
				>
					<span class="bg-background/90 text-muted-foreground rounded-full border p-2 shadow-sm">
						<span
							class="icon-[mdi--loading] block h-4 w-4 animate-spin motion-reduce:animate-none"
							aria-hidden="true"
						></span>
					</span>
					<span class="sr-only">Loading recent tasks…</span>
				</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
