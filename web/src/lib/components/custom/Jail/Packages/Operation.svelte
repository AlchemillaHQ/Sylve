<script lang="ts">
	import {
		getJailPackages,
		getPackageJob,
		getPackageLogs,
		startPackageOperation
	} from '$lib/api/jail/packages';
	import ModalTable from '$lib/components/custom/ModalTable.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { PackageJob, PackageView } from '$lib/types/jail/packages';
	import { packageReason, safeAdvisoryURL } from '$lib/types/jail/packages';
	import { isAPIResponse } from '$lib/utils/http';
	import { capitalizeFirstLetter, escapeHTML } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { onMount, untrack } from 'svelte';
	import { useInterval } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { ColumnDefinition, CellComponent } from 'tabulator-tables';

	let {
		open = $bindable(),
		node,
		ctId,
		taskId,
		afterChange
	}: {
		open: boolean;
		node: string;
		ctId: number;
		taskId: number;
		afterChange?: () => Promise<PackageView | null>;
	} = $props();
	let currentTask = $state(untrack(() => taskId));
	let job = $state<PackageJob | null>(null);
	let logs = $state('');
	let offset = 0;
	let logsComplete = false;
	let loading = $state(false);
	let error = $state('');
	let logError = $state('');
	let applying = $state(false);
	let snapshot = $state(false);
	let acknowledge = $state(false);
	let snapshotSupported = $state(false);
	let upgradeAvailable = $state(false);
	let upgradeReason = $state('Checking jail availability...');
	let now = $state(Date.now());
	let notifiedTask = 0;
	const controller = new AbortController();
	const options = () => ({ hostname: node, signal: controller.signal });
	const running = $derived(job === null || ['queued', 'running'].includes(job.status));
	const destructive = $derived(
		job?.plan?.transaction.changes.some((c) => ['remove', 'downgrade'].includes(c.action)) ?? false
	);
	const systemUpgrade = $derived(job?.scope === 'system');
	const needsAcknowledgement = $derived(systemUpgrade || destructive);
	const expired = $derived(job?.plan ? new Date(job.plan.expiresAt).getTime() <= now : true);
	const title = $derived.by(() => {
		switch (job?.action) {
			case 'pkg-preview':
				return systemUpgrade ? 'Jail System Upgrade Preview' : 'Upgrade Preview';
			case 'pkg-upgrade':
				return systemUpgrade ? 'Jail System Upgrade' : 'Package Upgrade';
			case 'pkg-check-updates':
				return 'Check Updates';
			default:
				return 'Package Audit';
		}
	});
	const icon = $derived(
		job?.action === 'pkg-audit'
			? 'icon-[mdi--shield-search]'
			: job?.action === 'pkg-check-updates'
				? 'icon-[mdi--update]'
				: 'icon-[mdi--package-up]'
	);
	const phaseLabels: Record<string, string> = {
		queued: 'Waiting to start',
		reading_inventory: 'Reading packages',
		auditing: 'Checking vulnerabilities',
		refreshing_repositories: 'Updating repository catalogs',
		solving: 'Resolving dependencies',
		verifying: 'Downloading and verifying packages',
		snapshotting: 'Creating snapshot',
		applying: 'Upgrading packages',
		post_upgrade_audit: 'Checking vulnerabilities',
		running: 'In progress',
		completed: 'Completed',
		failed: 'Failed'
	};
	const emptyPreview = $derived(
		job?.action === 'pkg-preview' &&
			((job.status === 'success' && !job.plan) ||
				job.error.split(':', 1)[0].trim() === 'no_eligible_packages')
	);
	const failed = $derived(!emptyPreview && (job?.status === 'failed' || job?.phase === 'failed'));
	const statusLabel = $derived.by(() => {
		if (!job) return 'Loading operation...';
		if (emptyPreview) return 'No eligible packages to upgrade';
		if (failed) return 'Operation failed';
		if (running) return phaseLabels[job.phase] || 'In progress';
		if (job.status === 'success') {
			switch (job.action) {
				case 'pkg-preview':
					return job.plan?.transaction.changes.length
						? 'Preview ready'
						: systemUpgrade
							? 'No system updates available'
							: 'No updates available';
				case 'pkg-upgrade':
					return 'Upgrade complete';
				case 'pkg-check-updates':
					return 'Update check complete';
				case 'pkg-audit': {
					const count = job.audit?.packages.length;
					return count === undefined
						? 'Audit complete'
						: count === 0
							? 'No known vulnerabilities found'
							: `${count} affected ${count === 1 ? 'package' : 'packages'}`;
				}
			}
		}
		return phaseLabels[job.phase] || 'Operation complete';
	});

	async function refresh(checkAvailability = false) {
		if (loading || controller.signal.aborted) return;
		loading = true;
		const requestedTask = currentTask;
		try {
			const result = job && !running ? job : await getPackageJob(ctId, requestedTask, options());
			if (controller.signal.aborted || requestedTask !== currentTask) return;
			if (isAPIResponse(result)) {
				error = packageReason(result.message, 'This package operation is unavailable.');
				upgradeAvailable = false;
				return;
			}
			job = result;
			error = '';
			const finished = !['queued', 'running'].includes(result.status);
			const completedNow = finished && notifiedTask !== requestedTask;
			if (completedNow) {
				notifiedTask = requestedTask;
			}
			if (
				finished &&
				(completedNow || checkAvailability) &&
				result.action === 'pkg-preview' &&
				result.status === 'success' &&
				(result.plan?.transaction.changes.length ?? 0) > 0 &&
				!expired
			) {
				const view = afterChange ? await afterChange() : await getJailPackages(ctId, options());
				if (controller.signal.aborted || requestedTask !== currentTask) return;
				upgradeAvailable = !!view && !isAPIResponse(view) && view.ready && !view.offline;
				snapshotSupported =
					upgradeAvailable && !!view && !isAPIResponse(view) && view.snapshotSupported;
				if (!snapshotSupported) snapshot = false;
				if (completedNow) {
					snapshot = result.scope === 'system' && snapshotSupported;
				}
				upgradeReason =
					!view || isAPIResponse(view)
						? packageReason(view?.message, 'Jail availability could not be checked.')
						: view.offline
							? 'Start the jail before applying these changes. This preview has not changed any packages.'
							: !view.ready
								? packageReason(view.reason || 'package_operation_in_progress')
								: '';
			} else if (completedNow) {
				await afterChange?.();
			}
			if (logsComplete) return;
			const chunk = await getPackageLogs(ctId, requestedTask, offset, options());
			if (controller.signal.aborted || requestedTask !== currentTask) return;
			if (isAPIResponse(chunk)) {
				logError = packageReason(chunk.message, 'Command output is unavailable.');
				logsComplete = finished;
				return;
			}
			logs = (logs + chunk.text).slice(-(1 << 20));
			logsComplete = finished && chunk.next === offset;
			offset = chunk.next;
			logError = '';
		} finally {
			loading = false;
		}
	}
	function checkAvailability() {
		if (open && !applying && !running && !expired && document.visibilityState === 'visible')
			void refresh(true);
	}
	onMount(() => {
		void refresh();
		return () => controller.abort();
	});
	useInterval(2000, {
		callback: () => {
			now = Date.now();
			if (open && !applying && (running || !logsComplete) && document.visibilityState === 'visible')
				void refresh();
		}
	});

	async function upgrade() {
		if (
			!job?.plan ||
			!upgradeAvailable ||
			loading ||
			applying ||
			expired ||
			(needsAcknowledgement && !acknowledge)
		)
			return;
		const requestedSnapshot = snapshot;
		applying = true;
		try {
			await refresh(true);
			if (!upgradeAvailable || expired || controller.signal.aborted) return;
			if (requestedSnapshot && !snapshotSupported) {
				toast.error(packageReason('package_snapshot_coverage_incomplete'), {
					position: 'bottom-center'
				});
				return;
			}
			const result = await startPackageOperation(
				ctId,
				'upgrade',
				{ planId: job.plan.id, snapshot: requestedSnapshot, acknowledge },
				options()
			);
			if (controller.signal.aborted) return;
			if (isAPIResponse(result)) {
				toast.error(packageReason(result.message), { position: 'bottom-center' });
				return;
			}
			currentTask = result.taskId;
			job = result;
			logs = '';
			offset = 0;
			logsComplete = false;
			logError = '';
			acknowledge = false;
			void afterChange?.();
		} finally {
			applying = false;
		}
	}

	const transactionColumns: ColumnDefinition[] = [
		{
			title: 'Action',
			field: 'action',
			formatter: (cell: CellComponent) => escapeHTML(capitalizeFirstLetter(String(cell.getValue())))
		},
		{ title: 'Package', field: 'name', formatter: 'plaintext', widthGrow: 2 },
		{ title: 'Installed', field: 'from', formatter: 'plaintext' },
		{ title: 'Proposed', field: 'to', formatter: 'plaintext' },
		{ title: 'Repository', field: 'repository', formatter: 'plaintext' }
	];
	const checkedColumns: ColumnDefinition[] = [
		{ title: 'Package', field: 'name', formatter: 'plaintext', widthGrow: 2 },
		{ title: 'Status', field: 'status', formatter: 'plaintext' }
	];
	const checkedRows = $derived(
		job?.plan?.selection.map((name) => ({ name, status: 'Up to date' })) ?? []
	);
	function advisoryFormatter(cell: CellComponent): HTMLElement {
		const row = cell.getRow().getData();
		const url = safeAdvisoryURL(String(row.url));
		const container = document.createElement('div');
		container.className = 'space-y-1 whitespace-normal';
		const description = document.createElement('p');
		description.textContent = String(row.description);
		container.append(description);
		const element = document.createElement(url ? 'a' : 'span');
		element.textContent = String(row.id);
		element.className = 'text-muted-foreground block text-xs break-all';
		if (url && element instanceof HTMLAnchorElement) {
			element.href = url;
			element.target = '_blank';
			element.rel = 'noopener noreferrer';
			element.classList.add('underline', 'underline-offset-2');
		}
		container.append(element);
		return container;
	}
	const auditColumns: ColumnDefinition[] = [
		{ title: 'Package', field: 'name', formatter: 'plaintext', minWidth: 130 },
		{ title: 'Version', field: 'version', formatter: 'plaintext', minWidth: 90 },
		{
			title: 'Advisory',
			field: 'id',
			formatter: advisoryFormatter,
			minWidth: 280,
			widthGrow: 3,
			variableHeight: true
		},
		{ title: 'CVE', field: 'cves', formatter: 'plaintext', minWidth: 150 }
	];
	const auditRows = $derived(
		job?.audit?.packages.flatMap((pkg) =>
			pkg.advisories.map((advisory) => ({
				...advisory,
				name: pkg.name,
				version: pkg.version,
				cves: advisory.cves.join(', ')
			}))
		) ?? []
	);
</script>

<svelte:window onfocus={checkAvailability} />
<svelte:document onvisibilitychange={checkAvailability} />

<Dialog.Root bind:open>
	<Dialog.Content class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
		<Dialog.Header class="border-b px-6 py-5 pr-12 text-left">
			<Dialog.Title>
				<SpanWithIcon {icon} size="h-5 w-5" gap="gap-2" {title} />
			</Dialog.Title>
			<Dialog.Description>{node} · Jail {ctId} · Task {currentTask}</Dialog.Description>
		</Dialog.Header>
		<div class="min-h-0 space-y-5 overflow-y-auto px-6 py-5 text-sm">
			{#if error}<p class="text-destructive" role="alert">{error}</p>{/if}
			{#if job || !error}
				<div class="space-y-2">
					<div class="flex flex-wrap items-center justify-between gap-2">
						<p
							class="flex items-center gap-2 font-medium"
							class:text-destructive={failed}
							role="status"
						>
							<span
								class="h-4 w-4 shrink-0 {running
									? 'icon-[mdi--loading] animate-spin'
									: failed
										? 'icon-[mdi--alert-circle-outline]'
										: job?.audit?.packages.length
											? 'icon-[mdi--shield-alert-outline]'
											: 'icon-[mdi--check-circle-outline]'}"
							></span>
							{statusLabel}
						</p>
						{#if job?.finishedAt}<span class="text-muted-foreground text-xs"
								>{convertDbTime(job.finishedAt)}</span
							>{/if}
					</div>
					{#if emptyPreview}
						<p class="text-muted-foreground text-xs">
							Application upgrades exclude locked and system packages. Use Updates → Jail system for
							pkgbase userland updates.
						</p>
					{:else if job?.status === 'success' && job.action === 'pkg-check-updates'}
						<p class="text-muted-foreground text-xs">
							Available versions are shown in the package list. No packages were changed.
						</p>
					{:else if job?.status === 'success' && job.action === 'pkg-upgrade'}
						<p class="text-muted-foreground text-xs">
							{systemUpgrade
								? 'Review .pkgnew and .pkgsave configuration files, then restart the jail and validate its services. Sylve does not restart it automatically.'
								: 'Review the command output for package messages. Restart affected services or the jail if needed; Sylve does not restart them automatically.'}
						</p>
					{:else if !failed && job?.plan && job.plan.transaction.changes.length > 0}
						<p class="text-muted-foreground text-xs">
							{#if systemUpgrade}FreeBSD {job.plan.system?.release} userland and its base dependencies
								only. Kernel, boot, package-manager, and application changes are excluded.{:else}Includes
								all dependency changes, removals, and downgrades.{/if}
						</p>
					{/if}
				</div>
			{/if}
			{#if job}
				{#if job.error && !emptyPreview}
					<div
						class="border-destructive/30 text-destructive space-y-2 rounded-md border px-3 py-2.5"
						role="alert"
					>
						<p>{packageReason(job.error)}</p>
						{#if job.action === 'pkg-upgrade'}<p class="text-xs">
								Some packages may have changed. Review the output and installed state before
								retrying.
							</p>{/if}
					</div>
				{/if}
				{#if job.snapshotId}<p class="text-muted-foreground text-xs">
						Pre-upgrade snapshot: #{job.snapshotId}
					</p>{/if}
				{#if job.status === 'success' && job.action === 'pkg-upgrade' && !job.audit}
					<p class="text-destructive">
						The upgrade completed, but the follow-up audit is unavailable. See the command output
						for details.
					</p>
				{/if}
				{#if job.plan}
					{#if job.plan.transaction.changes.length > 0}
						<section class="space-y-3">
							<div class="h-64">
								<ModalTable rows={job.plan.transaction.changes} columns={transactionColumns} />
							</div>
							<div class="text-muted-foreground flex flex-wrap gap-x-6 gap-y-1 text-xs">
								{#if job.plan.transaction.download}<span>{job.plan.transaction.download}</span>{/if}
								{#if job.plan.transaction.space}<span>{job.plan.transaction.space}</span>{/if}
								{#if job.action === 'pkg-preview'}<span
										>Expires {convertDbTime(job.plan.expiresAt)}</span
									>{/if}
							</div>
						</section>
					{:else if job.status === 'success' && job.action === 'pkg-preview' && checkedRows.length > 0}
						<div class="h-48"><ModalTable rows={checkedRows} columns={checkedColumns} /></div>
					{/if}
					{#if job.action === 'pkg-preview' && !running && job.status === 'success' && job.plan.transaction.changes.length > 0}
						<section class="space-y-4 border-t pt-4">
							{#if expired}<p class="text-destructive" role="alert">
									This preview has expired. Create a new preview to continue.
								</p>{:else if upgradeReason}<p class="text-muted-foreground">
									{upgradeReason}
								</p>{/if}
							<div class="grid items-center gap-3 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-x-6">
								<CustomCheckbox
									bind:checked={snapshot}
									disabled={!snapshotSupported || applying}
									label="Create a snapshot"
								/>
								{#if needsAcknowledgement}
									<CustomCheckbox
										bind:checked={acknowledge}
										disabled={applying}
										label={systemUpgrade
											? 'I acknowledge the jail system changes and manual restart requirements'
											: 'I acknowledge the listed removals and downgrades'}
									/>
								{/if}
							</div>
							{#if upgradeAvailable && !snapshotSupported}
								<p class="text-muted-foreground text-xs">
									Snapshot coverage is unavailable for this jail’s storage layout.
								</p>
							{/if}
						</section>
					{/if}
				{/if}
				{#if job.audit}
					<section class="space-y-3 {job.plan ? 'border-t pt-5' : ''}">
						{#if auditRows.length > 0}
							{#if job.action !== 'pkg-audit'}<h3 class="font-medium">
									Vulnerability findings
								</h3>{/if}
							<div class="h-64"><ModalTable rows={auditRows} columns={auditColumns} /></div>
						{:else if job.action !== 'pkg-audit'}<p>No known vulnerabilities found.</p>{/if}
						<p class="text-muted-foreground text-xs">
							Registered packages only{#if job.audit.databaseAt}, using advisories from {convertDbTime(
									job.audit.databaseAt
								)}{/if}; not a full base/kernel assessment.
						</p>
					</section>
				{/if}
			{/if}
			{#if logs || logError}
				<details class="border-t pt-4">
					<summary class="text-muted-foreground cursor-pointer text-xs font-medium select-none"
						>Command output</summary
					>
					<div class="mt-3 space-y-2">
						{#if logs}<pre
								class="bg-muted max-h-64 overflow-auto rounded-md border p-3 text-xs break-words whitespace-pre-wrap">{logs}</pre>{/if}
						{#if logError}<p class="text-destructive text-xs">{logError}</p>{/if}
					</div>
				</details>
			{/if}
		</div>
		{#if job?.action === 'pkg-preview' && job.status === 'success' && (job.plan?.transaction.changes.length ?? 0) > 0}
			<Dialog.Footer class="border-t px-6 py-4">
				<Button
					onclick={upgrade}
					disabled={!upgradeAvailable ||
						loading ||
						applying ||
						expired ||
						(needsAcknowledgement && !acknowledge)}
					>{applying ? 'Queuing...' : systemUpgrade ? 'Upgrade Jail System' : 'Upgrade'}</Button
				>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
