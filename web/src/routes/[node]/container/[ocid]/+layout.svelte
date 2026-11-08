<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import {
		containerAction,
		deleteContainer,
		getContainer,
		getContainers,
		updateContainer,
		updateContainerOptions
	} from '$lib/api/container/container';
	import { getActiveLifecycleTaskForGuest } from '$lib/api/task/lifecycle';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { reload } from '$lib/stores/api.svelte';
	import { confirmGuestAction } from '$lib/stores/guest-actions.svelte';
	import type { GFSStep } from '$lib/types/common';
	import {
		containerActionLabel,
		containerTitle,
		type ContainerAction,
		type ContainerContext,
		type ContainerDetail
	} from '$lib/types/container/container';
	import { getAPIErrorText, isAPIResponse, removeCache, updateCache } from '$lib/utils/http';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { specProblem } from '$lib/utils/container/validate';
	import { resource, useInterval, watch } from 'runed';
	import { setContext, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		data,
		children
	}: { data: { node: string; ocId: number; container: ContainerDetail }; children?: Snippet } =
		$props();

	const container = resource(
		[() => data.node, () => data.ocId],
		async ([hostname, ocId], _, { signal }) => {
			const result = await getContainer(ocId, { hostname, signal });
			if (isAPIResponse(result)) throw new Error(getAPIErrorText(result));
			await updateCache(`container-${ocId}`, result, hostname);
			return result;
		}
	);
	const task = resource([() => data.node, () => data.ocId], async ([hostname, ocId]) => {
		const result = await getActiveLifecycleTaskForGuest('container', ocId, hostname);
		return isAPIResponse(result) ? null : result;
	});

	let current = $derived(
		container.current?.ocId === data.ocId ? container.current : data.container
	);
	let runtimeState = $derived(current.runtime?.state ?? 'missing');
	let running = $derived(runtimeState === 'running');

	let webUrl = $derived.by(() => {
		if (!running || !current.webPort) return '';
		const scheme = current.webHttps ? 'https' : 'http';
		const host = data.node === storage.localHostname ? location.hostname : data.node;
		if (!current.network) return `${scheme}://${host}:${current.webPort}`;
		if (current.network.switchId) {
			const ip = current.runtime?.ips[0] || current.ip;
			return ip ? `${scheme}://${ip}:${current.webPort}` : '';
		}
		const port = current.ports.find(
			(port) => port.containerPort === current.webPort && port.protocol === 'tcp'
		);
		if (!port) return '';
		const address = port.hostIp && port.hostIp !== '0.0.0.0' ? port.hostIp : host;
		return `${scheme}://${address}:${port.hostPort}`;
	});
	let pendingAction = $derived(task.current?.action ?? '');

	// Polls and refresh events share a running refetch: starting another would
	// abort it, and podman answers late while it's busy with the container.
	let refetching: Promise<unknown> | undefined;

	const context: ContainerContext = {
		get current() {
			return current;
		},
		get pendingAction() {
			return pendingAction;
		},
		get busy() {
			return !!pendingAction || current.creationPending;
		},
		refetch: () =>
			(refetching ??= Promise.all([container.refetch(), task.refetch()]).finally(() => {
				refetching = undefined;
			})),
		async update(spec) {
			const problem = specProblem(spec);
			if (problem) {
				toast.error(problem.message, { position: 'bottom-center' });
				return false;
			}
			const result = await updateContainer(data.ocId, spec, data.node);
			return settle(result, 'Container update queued; it will be recreated');
		},
		async setOptions(options) {
			const result = await updateContainerOptions(data.ocId, options, data.node);
			return settle(result, 'Container updated');
		}
	};
	setContext('container', context);

	async function settle(result: unknown, success: string) {
		if (isAPIResponse(result) && result.status === 'error') {
			toastContainerError(result);
			return false;
		}
		toast.success(success, { position: 'bottom-center' });
		reload.leftPanel = true;
		await context.refetch();
		return true;
	}

	const summaryBar = $state({ step: 'hourly' as GFSStep, showLogs: false });
	setContext('containerSummaryBar', summaryBar);

	let section = $derived(page.url.pathname.split('/')[4] ?? 'summary');
	let inFlight = $state(false);
	let deleteDialog = $state({ open: false, volumes: false, stack: false });
	const stackMembers = resource(
		() => (deleteDialog.open && current.stack ? data.node : ''),
		async (hostname) => {
			if (!hostname) return [];
			const result = await getContainers(hostname);
			return result.filter((item) => item.stack === current.stack);
		},
		{ initialValue: [] }
	);

	watch(
		() => reload.datacenterNodesPulse,
		() => {
			if (!inFlight) context.refetch();
		},
		{ lazy: true }
	);
	// A task is marked finished after its last refresh event, so follow it until then.
	useInterval(1000, {
		callback: () => {
			if (context.busy && !inFlight) context.refetch();
		}
	});

	async function act(action: ContainerAction) {
		if (
			!(await confirmGuestAction({
				guestType: 'container',
				guestId: current.ocId,
				name: containerTitle(current),
				hostname: data.node,
				action: action === 'upgrade' || action === 'upgrade-stack' ? 'update' : action
			}))
		)
			return;
		inFlight = true;
		const result = await containerAction(current.ocId, action, data.node);
		if (isAPIResponse(result)) {
			toastContainerError(result);
		} else {
			reload.leftPanel = true;
		}
		await context.refetch();
		inFlight = false;
	}

	async function remove() {
		deleteDialog.open = false;
		inFlight = true;
		const { ocId, stack, stackTitle } = current;
		const name = containerTitle(current);
		const whole = deleteDialog.stack && stackMembers.current.length > 1;
		const ocIds = whole ? stackMembers.current.map((member) => member.ocId) : [ocId];
		for (const member of ocIds) {
			const result = await deleteContainer(member, deleteDialog.volumes, data.node);
			if (isAPIResponse(result)) {
				inFlight = false;
				toastContainerError(result);
				return;
			}
			await removeCache(`container-${member}`, data.node);
		}
		inFlight = false;
		toast.success(
			whole ? `Stack ${stackTitle || stack} deletion queued` : `Container ${name} deletion queued`,
			{
				position: 'bottom-center'
			}
		);
		reload.leftPanel = true;
		await useSafeGoto(`/${data.node}/summary`);
	}
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	<div class="flex h-10 w-full shrink-0 items-center justify-between gap-1 border p-4">
		{#if section !== 'summary'}
			<div class="flex min-w-0 items-center gap-2">
				<Badge variant="outline" class="text-muted-foreground px-1.5" title={runtimeState}>
					{#if running}
						<span class="icon-[mdi--check-circle] text-green-500"></span>
					{:else}
						<span class="icon-[mdi--close-circle] text-gray-500"></span>
					{/if}
				</Badge>
				<p class="truncate text-sm font-semibold">{containerTitle(current)} ({current.ocId})</p>
			</div>
		{/if}

		<div class="flex items-center gap-1">
			{#if pendingAction}
				<Badge variant="outline" class="px-1.5 text-xs">
					<span class="icon-[mdi--loading] mr-1 h-3 w-3 animate-spin"></span>
					{containerActionLabel(pendingAction)}
				</Badge>
			{:else if running}
				{#each [['restart', 'icon-[mdi--restart]', 'Restart'], ['stop', 'icon-[mdi--stop]', 'Stop'], ['kill', 'icon-[mdi--skull-outline]', 'Kill']] as [action, icon, title] (action)}
					<Button
						onclick={() => act(action as ContainerAction)}
						disabled={inFlight}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-yellow-600 dark:text-white"
					>
						<SpanWithIcon {icon} size="h-4 w-4" gap="gap-1" {title} />
					</Button>
				{/each}
			{:else}
				<Button
					onclick={() => act('start')}
					disabled={inFlight || current.creationPending}
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-green-600 dark:text-white"
				>
					<SpanWithIcon icon="icon-[mdi--play]" size="h-4 w-4" gap="gap-1" title="Start" />
				</Button>
				<Button
					onclick={() => (deleteDialog = { open: true, volumes: false, stack: false })}
					disabled={inFlight || current.creationPending}
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted ml-2 h-6 text-black hover:bg-red-600 dark:text-white"
				>
					<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
				</Button>
			{/if}
		</div>

		{#if section === 'summary'}
			<div class="ml-auto flex items-center gap-2">
				{#if webUrl}
					<Button
						size="sm"
						href={webUrl}
						target="_blank"
						rel="noopener noreferrer"
						title={webUrl}
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
					>
						<SpanWithIcon
							icon="icon-[mdi--open-in-new]"
							size="h-4 w-4"
							gap="gap-1"
							title="Open Web UI"
						/>
					</Button>
				{/if}
				{#if current.updateDigest && !pendingAction}
					<Button
						onclick={() => act('upgrade')}
						disabled={inFlight}
						size="sm"
						title="A newer {current.image} is available"
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
					>
						<SpanWithIcon
							icon="icon-[mdi--arrow-up-circle-outline]"
							size="h-4 w-4"
							gap="gap-1"
							title="Update"
						/>
					</Button>
				{/if}
				<Button
					size="sm"
					onclick={() => (summaryBar.showLogs = true)}
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
				>
					<SpanWithIcon
						icon="icon-[mdi--file-document-outline]"
						size="h-4 w-4"
						gap="gap-1"
						title="View Logs"
					/>
				</Button>
				<SimpleSelect
					options={[
						{ label: 'Hourly', value: 'hourly' },
						{ label: 'Daily', value: 'daily' },
						{ label: 'Weekly', value: 'weekly' },
						{ label: 'Monthly', value: 'monthly' },
						{ label: 'Yearly', value: 'yearly' }
					]}
					bind:value={summaryBar.step}
					onChange={(value) => (summaryBar.step = value as GFSStep)}
					classes={{ trigger: 'h-6!' }}
					icon="icon-[mdi--calendar]"
				/>
			</div>
		{/if}
	</div>

	<div
		class="min-h-0 flex-1"
		class:overflow-hidden={section === 'console'}
		class:overflow-auto={section !== 'console'}
	>
		{#key `${data.node}:${data.ocId}`}
			{@render children?.()}
		{/key}
	</div>
</div>

<AlertDialog.Root bind:open={deleteDialog.open}>
	<AlertDialog.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialog.Header>
			<AlertDialog.Title>Are you sure?</AlertDialog.Title>
			<AlertDialog.Description>
				This will permanently delete the container
				<span class="font-semibold">{containerTitle(current)} ({current.ocId}).</span>
				{#if stackMembers.current.length > 1}
					<CustomCheckbox
						label="Delete the whole {current.stackTitle || current.stack} stack ({stackMembers
							.current.length} containers)"
						bind:checked={deleteDialog.stack}
						classes="mt-4 flex items-center gap-2"
					/>
				{/if}
				<CustomCheckbox
					label="Delete Volumes"
					bind:checked={deleteDialog.volumes}
					classes="mt-4 flex items-center gap-2"
				/>
				{#if !deleteDialog.volumes && current.mounts.some((mount) => mount.type === 'volume')}
					<div
						class="mt-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
					>
						<span
							class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400"
							aria-hidden="true"
						></span>
						<div>
							<p class="font-medium text-amber-600 dark:text-amber-400">Volumes retained</p>
							<p class="text-muted-foreground mt-0.5">
								The volumes stay at {current.pool}/sylve/containers/{current.ocId}. A new container
								with this ID will use them again.
							</p>
						</div>
					</div>
				{/if}
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={remove}>Delete</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
