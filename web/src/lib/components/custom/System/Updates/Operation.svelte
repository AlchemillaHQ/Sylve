<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import {
		cancelHostUpdate,
		getHostUpdateJob,
		getHostUpdateLogs,
		startHostUpdate
	} from '$lib/api/system/updates';
	import ModalTable from '$lib/components/custom/ModalTable.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { connection, planRestart } from '$lib/stores/api.svelte';
	import {
		hostUpdateActionLabels,
		hostUpdateReason,
		type HostUpdateJob
	} from '$lib/types/system/updates';
	import { isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { useInterval, watch } from 'runed';
	import { onMount, untrack } from 'svelte';
	import type { ColumnDefinition } from 'tabulator-tables';

	let {
		open = $bindable(),
		node,
		jobId,
		bootEnvironmentSupported,
		afterChange
	}: {
		open: boolean;
		node: string;
		jobId: number;
		bootEnvironmentSupported: boolean;
		afterChange: () => Promise<void>;
	} = $props();
	let currentId = $state(untrack(() => jobId));
	let job = $state<HostUpdateJob | null>(null);
	let logs = $state('');
	let logsOpen = $state(false);
	let error = $state('');
	let loading = $state(false);
	let applying = $state(false);
	let cancelling = $state(false);
	let acknowledge = $state(false);
	let bootEnvironment = $state(untrack(() => bootEnvironmentSupported));
	let now = $state(Date.now());
	let offset = 0;
	let logsComplete = false;
	let unavailable = $state(false);
	let reconnecting = $state(false);
	let ownsRestart = false;
	// While an installation runs, these mean the node is unreachable, usually
	// because Sylve is restarting, not that the operation failed.
	const connectionFailures = ['Request failed', 'Invalid response format', 'node_unreachable'];
	const controller = new AbortController();
	const options = () => ({ hostname: node, signal: controller.signal });
	const running = $derived(!unavailable && (!job || ['queued', 'running'].includes(job.status)));
	const installing = $derived(running && !!job?.action.endsWith('-apply'));
	const restartsSylve = (plan: HostUpdateJob['plan']) =>
		plan?.kind === 'sylve' || !!plan?.transaction.changes.some((change) => change.name === 'sylve');
	const cancellable = $derived(running && !!job && !job.action.endsWith('-apply'));
	const expired = $derived(!job?.plan || new Date(job.plan.expiresAt).getTime() <= now);
	const preview = $derived(
		job?.action.endsWith('-preview') && job.status === 'success' && !!job.plan
	);
	const sylveUpdate = $derived(job?.plan?.kind === 'sylve');
	const failed = $derived(unavailable || job?.status === 'failed');
	watch(
		() => failed,
		(value) => {
			if (value) logsOpen = true;
		}
	);
	const title = $derived(
		job ? (hostUpdateActionLabels[job.action] ?? 'Host Update') : 'Host Update'
	);
	const icon = $derived(
		job?.action === 'audit'
			? 'icon-[mdi--shield-search]'
			: job?.action.startsWith('packages-')
				? 'icon-[mdi--package-up]'
				: 'icon-[mdi--update]'
	);
	const columns: ColumnDefinition[] = [
		{ title: 'Action', field: 'action', formatter: 'plaintext' },
		{ title: 'Package', field: 'name', formatter: 'plaintext', widthGrow: 2 },
		{ title: 'Installed', field: 'from', formatter: 'plaintext' },
		{ title: 'Proposed', field: 'to', formatter: 'plaintext' },
		{ title: 'Repository', field: 'repository', formatter: 'plaintext' }
	];
	const phases: Record<string, string> = {
		queued: 'Waiting to start',
		reading_inventory: 'Reading packages',
		auditing: 'Auditing packages',
		refreshing_repositories: 'Refreshing repositories',
		solving: 'Resolving updates',
		verifying: 'Downloading and verifying packages',
		snapshotting: 'Creating boot environment',
		downloading: 'Downloading release',
		checking_release: 'Checking release',
		applying: 'Preparing and applying update',
		restarting: 'Restarting Sylve — waiting to reconnect',
		completed: 'Completed',
		failed: 'Failed'
	};

	async function refresh() {
		if (loading || controller.signal.aborted) return;
		loading = true;
		try {
			const result = await getHostUpdateJob(currentId, options());
			if (controller.signal.aborted) return;
			if (isAPIResponse(result)) {
				reconnecting = installing && connectionFailures.includes(result.message ?? '');
				error = reconnecting ? '' : hostUpdateReason(result.message);
				unavailable = !job;
				return;
			}
			reconnecting = false;
			const ranHere = !!job && ['queued', 'running'].includes(job.status);
			job = result;
			error = '';
			const finished = !['queued', 'running'].includes(result.status);
			if (finished && ranHere) void afterChange();
			// Finished without Sylve going down, so no restart is coming.
			if (finished && ownsRestart && !connection.plannedRestart?.disconnectObserved) {
				connection.plannedRestart = null;
				ownsRestart = false;
			}
			if (logsComplete) return;
			const chunk = await getHostUpdateLogs(currentId, offset, options());
			if (controller.signal.aborted) return;
			if (isAPIResponse(chunk)) {
				reconnecting = installing && connectionFailures.includes(chunk.message ?? '');
				if (!reconnecting) error = hostUpdateReason(chunk.message);
				return;
			}
			logs = (logs + chunk.text).slice(-(1 << 20));
			logsComplete = finished && chunk.next === offset;
			offset = chunk.next;
		} catch (cause) {
			if (!isRequestCancellation(cause)) throw cause;
		} finally {
			loading = false;
		}
	}
	onMount(() => {
		void refresh();
		return () => controller.abort();
	});
	useInterval(2000, {
		callback: () => {
			now = Date.now();
			if (
				open &&
				!applying &&
				!unavailable &&
				(running || !logsComplete) &&
				document.visibilityState === 'visible'
			)
				void refresh();
		}
	});
	async function apply() {
		const plan = job?.plan;
		if (!plan || !acknowledge || expired || applying || loading || running) return;
		applying = true;
		// Restarting the node that serves this page drops the UI's connection: show
		// the restart overlay, and reopen this operation after the automatic reload.
		if (restartsSylve(plan) && node === storage.localHostname)
			ownsRestart = planRestart(
				'Sylve is restarting to finish the update. The page will refresh automatically.'
			);
		try {
			const result = await startHostUpdate(
				plan.kind === 'sylve' ? 'sylve-apply' : 'packages-apply',
				{
					planId: plan.id,
					acknowledge,
					bootEnvironment: plan.kind === 'packages' && bootEnvironmentSupported && bootEnvironment
				},
				options()
			);
			if (controller.signal.aborted) return;
			if (isAPIResponse(result)) {
				error = hostUpdateReason(result.message);
				if (ownsRestart) connection.plannedRestart = null;
				ownsRestart = false;
				return;
			}
			currentId = result.id;
			if (ownsRestart) {
				const tab = resolve(
					plan.kind === 'sylve'
						? '/[node]/system/updates/sylve'
						: '/[node]/system/updates/packages',
					{ node }
				);
				// eslint-disable-next-line svelte/no-navigation-without-resolve
				replaceState(`${tab}?job=${result.id}`, page.state);
			}
			job = result;
			logs = '';
			offset = 0;
			logsComplete = false;
			acknowledge = false;
			error = '';
			await afterChange();
		} catch (cause) {
			if (!isRequestCancellation(cause)) throw cause;
		} finally {
			applying = false;
		}
	}
	async function cancel() {
		if (!job || cancelling) return;
		cancelling = true;
		try {
			const result = await cancelHostUpdate(job.id, options());
			if (isAPIResponse(result) && !controller.signal.aborted) {
				error = hostUpdateReason(result.message);
				cancelling = false;
			}
		} catch (cause) {
			cancelling = false;
			if (!isRequestCancellation(cause)) throw cause;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl"
		controlsClass="top-5"
	>
		<Dialog.Header class="border-b px-6 py-5 pr-12 text-left">
			<Dialog.Title><SpanWithIcon {icon} size="h-5 w-5" gap="gap-2" {title} /></Dialog.Title>
			<Dialog.Description>{node} · Operation {currentId}</Dialog.Description>
		</Dialog.Header>
		<div class="min-h-0 space-y-5 overflow-y-auto px-6 py-5 text-sm">
			<div class="flex flex-wrap items-center justify-between gap-2">
				<p
					role="status"
					class="flex items-center gap-2 font-medium"
					class:text-destructive={failed}
				>
					<span
						class="h-4 w-4 shrink-0 {running
							? 'icon-[mdi--loading] animate-spin'
							: failed
								? 'icon-[mdi--alert-circle-outline]'
								: 'icon-[mdi--check-circle-outline]'}"
					></span>
					{#if preview}
						{sylveUpdate ? 'Ready to update' : 'Preview ready'}
					{:else if !job}
						{unavailable ? 'Operation unavailable' : 'Loading operation…'}
					{:else if reconnecting}
						{restartsSylve(job.plan)
							? 'Sylve is restarting. Reconnecting…'
							: 'Connection interrupted. Reconnecting…'}
					{:else if job.phase === 'verifying' && sylveUpdate}
						Verifying release
					{:else}
						{phases[job.phase] ?? job.phase}
					{/if}
				</p>
				{#if job?.finishedAt}<span class="text-muted-foreground text-xs"
						>{convertDbTime(job.finishedAt)}</span
					>{/if}
			</div>
			{#if error}<p class="text-destructive" role="alert">{error}</p>{/if}
			{#if job?.error}<div
					class="border-destructive/30 space-y-2 rounded-md border p-3"
					role="alert"
				>
					<p class="text-destructive">{hostUpdateReason(job.error)}</p>
					{#if job.action === 'sylve-apply'}<p class="text-muted-foreground text-xs">
							The executable may have changed. Review the operation log before retrying. No
							automatic rollback is performed.
						</p>{:else if job.action.endsWith('-apply')}<p class="text-muted-foreground text-xs">
							Some packages may have changed. Review command output and installed state before
							retrying. No automatic retry or downgrade is performed.
						</p>{/if}
				</div>{/if}
			{#if job?.result}<p class="text-muted-foreground whitespace-pre-wrap">{job.result}</p>{/if}
			{#if job?.status === 'success' && job.action === 'check'}
				<p class="text-muted-foreground text-xs">
					Available versions are shown in Packages. Nothing was installed.
				</p>
			{/if}
			{#if job?.plan?.kind === 'packages'}
				{#if job.plan.selection.length === 1 && ['pkg', 'pkg-devel'].includes(job.plan.selection[0])}<p
						class="text-muted-foreground text-xs"
					>
						pkg must be upgraded on its own first. Upgrade it, then preview the remaining packages
						again.
					</p>{/if}
				<section class="space-y-2">
					<div class="h-64"><ModalTable rows={job.plan.transaction.changes} {columns} /></div>
					{#if job.plan.transaction.download || job.plan.transaction.space}<p
							class="text-muted-foreground text-xs"
						>
							{[job.plan.transaction.download, job.plan.transaction.space]
								.filter(Boolean)
								.join(' ')}
						</p>{/if}
				</section>
			{:else if job?.plan?.release}
				<dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-2">
					<dt>Release</dt>
					<dd>
						{job.plan.release.channel === 'rolling' ? 'Rolling' : 'Tagged'}
						{job.plan.release.version}
					</dd>
					<dt>Commit</dt>
					<dd class="break-all font-mono text-xs">{job.plan.release.commit}</dd>
					<dt>Built at</dt>
					<dd title={job.plan.release.builtAt}>
						{convertDbTime(job.plan.release.builtAt, 'yyyy-MM-dd hh:mm:ss a')}
					</dd>
					<dt>SHA-256</dt>
					<dd class="break-all font-mono text-xs">{job.plan.release.sha256}</dd>
					<dt>Built for</dt>
					<dd>FreeBSD {job.plan.release.freebsd}</dd>
				</dl>
			{/if}
			{#if preview}
				<section class="space-y-4 border-t pt-4">
					{#if sylveUpdate}<p class="text-muted-foreground text-xs">
							Expires {job?.plan ? convertDbTime(job.plan.expiresAt) : ''}. Nothing has been
							installed.
						</p>
						<p class="text-muted-foreground text-xs leading-relaxed">
							Sylve will restart to apply this update. Replication-protected guests may stop or fail
							over. Back up configuration and data first; no automatic rollback is performed.
						</p>
					{/if}
					<div class="grid items-center gap-3 {sylveUpdate ? '' : 'sm:grid-cols-2 sm:gap-x-6'}">
						{#if !sylveUpdate}
							{#if bootEnvironmentSupported}<CustomCheckbox
									bind:checked={bootEnvironment}
									label="Create boot environment"
									disabled={applying}
								/>{:else}<p class="text-muted-foreground text-xs">
									Boot environment unavailable.
								</p>{/if}
						{/if}
						<CustomCheckbox
							bind:checked={acknowledge}
							label={sylveUpdate
								? 'I acknowledge the restart and maintenance impact'
								: 'I acknowledge the upgrade impact'}
							disabled={applying}
						/>
					</div>
					{#if expired}<p class="text-destructive">
							This preview expired. Request a fresh preview.
						</p>{/if}
				</section>
			{/if}
			{#if logs}<details bind:open={logsOpen} class="border-t pt-4">
					<summary class="text-muted-foreground cursor-pointer text-xs font-medium select-none"
						>Command output</summary
					>
					<pre
						class="bg-muted mt-3 max-h-64 overflow-auto rounded-md border p-3 font-mono text-xs break-words whitespace-pre-wrap">{logs}</pre>
				</details>{/if}
		</div>
		{#if cancellable}<Dialog.Footer class="border-t px-6 py-4">
				<Button variant="outline" disabled={cancelling} onclick={cancel}
					>{cancelling ? 'Stopping…' : 'Stop'}</Button
				>
			</Dialog.Footer>{:else if preview}<Dialog.Footer class="border-t px-6 py-4">
				<Button disabled={!acknowledge || expired || applying || loading} onclick={apply}
					>{applying ? 'Starting…' : sylveUpdate ? 'Update & restart' : 'Upgrade'}</Button
				>
			</Dialog.Footer>{/if}
	</Dialog.Content>
</Dialog.Root>
