<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import * as Accordion from '$lib/components/ui/accordion/index.js';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { migrateVM, migrateJail, validateMigration, cancelMigration } from '$lib/api/migration';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { ValidateResult } from '$lib/types/migration';
	import { getRecentLifecycleTasks } from '$lib/api/task/lifecycle';
	import { isAPIResponse } from '$lib/utils/http';
	import { onMount } from 'svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { sleep } from '$lib/utils';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	let {
		open = $bindable(false),
		guestType,
		guestId,
		guestName = '',
		node,
		sourceNodeUuid = '',
		onSuccess = () => {}
	}: {
		open: boolean;
		guestType: 'vm' | 'jail';
		guestId: number;
		guestName?: string;
		node: string;
		sourceNodeUuid?: string;
		onSuccess?: (targetHostname: string) => void;
	} = $props();

	const PHASE_ORDER = [
		'preflight',
		'initial_replication',
		'stop_source',
		'final_sync',
		'start_target',
		'policy_adjustment',
		'cleanup_source',
		'finalize'
	] as const;

	const PHASE_LABELS: Record<string, string> = {
		preflight: 'Preflight check',
		initial_replication: 'Initial replication',
		stop_source: 'Stop source',
		final_sync: 'Final sync',
		start_target: 'Start on target',
		policy_adjustment: 'Policy adjustment',
		cleanup_source: 'Cleanup source',
		finalize: 'Finalize'
	};

	function formatMessage(s: string): string {
		return s
			.replace(/_/g, ' ')
			.replace(/\b\w/g, (c) => c.toUpperCase())
			.replace(/Warning /gi, '⚠ ');
	}

	function formatDetail(s: string): string {
		return s.replace(/_/g, ' ').replace(/replicating_dataset:/, 'Replicating: ');
	}

	let nodes = $state<ClusterNode[]>([]);
	let selectedNodeUuid = $state('');
	let validation = $state<ValidateResult | null>(null);
	let loading = $state(false);
	let validating = $state(false);
	let error = $state('');
	let migrationTaskId = $state<number | null>(null);
	let pollingInterval = $state<ReturnType<typeof setInterval> | null>(null);
	let migrationStatus = $state<string>('');
	let migrating = $state(false);
	let currentPhase = $state('');
	let targetHostname = $state('');
	let sourceNode = $state('');

	watch(
		() => open,
		(o) => {
			if (o) {
				resumeExistingMigration();
			} else {
				stopPolling();
			}
		}
	);

	async function resumeExistingMigration() {
		if (pollingInterval) return;

		const freshNodes = await getNodes();
		const onlineNodes: ClusterNode[] = !isAPIResponse(freshNodes) ? freshNodes : [];

		// Try the current node first, then all other online cluster nodes.
		const candidates = [node];
		for (const n of onlineNodes) {
			if (n.hostname) {
				const isSelf = sourceNodeUuid
					? n.nodeUUID === sourceNodeUuid
					: n.hostname.toLowerCase() === node.toLowerCase();
				if (!isSelf) {
					candidates.push(n.hostname);
				}
			}
		}

		for (const hostname of candidates) {
			const tasks = await getRecentLifecycleTasks(10, guestType, guestId, hostname);
			if (!tasks || isAPIResponse(tasks) || !Array.isArray(tasks) || tasks.length === 0) {
				continue;
			}

			// For completed migrations on this node, redirect immediately.
			const doneTask = tasks.find(
				(t) => t.action === 'migrate' && t.status === 'success' && t.id === migrationTaskId
			);

			if (doneTask && hostname === node) {
				let th = '';
				try {
					if (doneTask.payload) {
						const p = JSON.parse(doneTask.payload);
						if (p.targetNodeUuid) {
							const n = onlineNodes.find((x) => x.nodeUUID === p.targetNodeUuid);
							if (n) th = n.hostname || '';
						}
					}
				} catch {
					/* ignore */
				}
				if (th) {
					open = false;
					onSuccess(th);
					return;
				}
			}

			// For in-progress migrations, resume the progress view.
			const activeTask = tasks.find(
				(t) => t.action === 'migrate' && (t.status === 'queued' || t.status === 'running')
			);
			if (activeTask) {
				migrationTaskId = activeTask.id;
				sourceNode = hostname;

				// Resolve target hostname from payload for post-migration redirect
				try {
					if (activeTask.payload && !targetHostname) {
						const p = JSON.parse(activeTask.payload);
						if (p.targetNodeUuid) {
							const n = onlineNodes.find((x) => x.nodeUUID === p.targetNodeUuid);
							if (n) targetHostname = n.hostname || '';
						}
					}
				} catch {
					/* ignore */
				}

				migrating = true;
				startPolling();
				return;
			}
		}
	}

	onMount(async () => {
		const result = await getNodes();
		if (!isAPIResponse(result)) {
			const selfUuid = sourceNodeUuid;
			const selfHostname = node.toLowerCase();
			nodes = result.filter(
				(n) =>
					n.nodeUUID !== '' &&
					n.status === 'online' &&
					n.nodeUUID !== selfUuid &&
					(selfUuid === '' ? n.hostname.toLowerCase() !== selfHostname : true)
			);
		}
	});

	async function onTargetSelect(nodeUuid: string) {
		if (nodeUuid === selectedNodeUuid && validation) return;
		selectedNodeUuid = nodeUuid;
		error = '';
		validation = null;
		validating = true;

		await sleep(1500);

		const result = await validateMigration(guestType, guestId, nodeUuid, node);
		if (isAPIResponse(result)) {
			error = String(result.error || 'Validation failed');
		} else {
			validation = result;
		}
		validating = false;
	}

	async function doMigrate() {
		if (!selectedNodeUuid) return;

		loading = true;
		error = '';

		const selected = nodes.find((n) => n.nodeUUID === selectedNodeUuid);
		targetHostname = selected?.hostname || '';

		let result;
		if (guestType === 'vm') {
			result = await migrateVM(guestId, selectedNodeUuid, node);
		} else {
			result = await migrateJail(guestId, selectedNodeUuid, node);
		}

		if (isAPIResponse(result)) {
			error = String(result.error || result.message || 'Migration request failed');
			loading = false;
			return;
		}

		migrationTaskId = result.taskId;
		sourceNode = node;
		migrating = true;
		startPolling();
	}

	function startPolling() {
		stopPolling();
		pollingInterval = setInterval(pollMigrationStatus, 3000);
		pollMigrationStatus();
	}

	function stopPolling() {
		if (pollingInterval) {
			clearInterval(pollingInterval);
			pollingInterval = null;
		}
	}

	async function pollMigrationStatus() {
		if (!migrationTaskId) return;

		const tasks = await getRecentLifecycleTasks(10, guestType, guestId, sourceNode || node);
		if (!tasks || isAPIResponse(tasks) || !Array.isArray(tasks) || tasks.length === 0) {
			return;
		}

		const task = tasks.find((t) => t.id === migrationTaskId);
		if (!task) {
			return;
		}

		migrationStatus = task.message || task.status;
		loading = false;

		// Parse phase from payload
		try {
			if (task.payload) {
				const payload = JSON.parse(task.payload);
				if (payload.phase) {
					currentPhase = payload.phase;
				}
				if (payload.targetNodeUuid && !targetHostname) {
					const target = nodes.find((n) => n.nodeUUID === payload.targetNodeUuid);
					if (target) targetHostname = target.hostname || '';
				}
			}
		} catch {
			// payload may not be valid JSON
		}

		if (task.status === 'success') {
			stopPolling();
			migrationStatus = 'Migration completed successfully';
			setTimeout(() => {
				open = false;
				onSuccess(targetHostname);
				resetState();
			}, 2000);
		} else if (task.status === 'failed') {
			stopPolling();
			migrationStatus = 'Migration failed';
			error = task.error || 'Migration failed';
		}
	}

	async function doCancelMigration() {
		if (!migrationTaskId) return;

		const result = await cancelMigration(migrationTaskId, sourceNode || node);
		if (!result || result.status !== 'success') {
			error = String(result?.error || 'Cancel failed');
		} else {
			stopPolling();
			migrationStatus = 'Migration cancelled';
			setTimeout(() => {
				open = false;
				resetState();
			}, 2000);
		}
	}

	function resetState() {
		selectedNodeUuid = '';
		validation = null;
		loading = false;
		validating = false;
		error = '';
		migrationTaskId = null;
		migrationStatus = '';
		migrating = false;
		currentPhase = '';
		targetHostname = '';
		sourceNode = '';
		stopPolling();
	}

	function parseProgressPercent(): number {
		const pctMatch = migrationStatus.match(/\((\d+)%\)/);
		return pctMatch ? parseInt(pctMatch[1], 10) : -1;
	}

	function phaseStatus(phase: string): 'done' | 'active' | 'pending' {
		const ci = PHASE_ORDER.indexOf(
			currentPhase as
				| 'preflight'
				| 'initial_replication'
				| 'stop_source'
				| 'final_sync'
				| 'start_target'
				| 'policy_adjustment'
				| 'cleanup_source'
				| 'finalize'
		);
		const pi = PHASE_ORDER.indexOf(
			phase as
				| 'preflight'
				| 'initial_replication'
				| 'stop_source'
				| 'final_sync'
				| 'start_target'
				| 'policy_adjustment'
				| 'cleanup_source'
				| 'finalize'
		);
		if (ci >= 0 && pi >= 0) {
			if (pi < ci) return 'done';
			if (pi === ci) return 'active';
		}
		return 'pending';
	}

	function phaseDetail(phase: string): string {
		if (phase === currentPhase) {
			const detail = migrationStatus || '';
			const colonIdx = detail.indexOf(':');
			if (colonIdx >= 0) {
				const after = detail.substring(colonIdx + 1);
				const pctIdx = after.indexOf('(');
				if (pctIdx >= 0) return formatDetail(after.substring(0, pctIdx).trim());
				return formatDetail(after.trim());
			}
		}
		return '';
	}

	function guestTypeLabel(): string {
		return guestType === 'vm' ? 'VM' : 'Jail';
	}

	function formatStatus(status: string): string {
		if (status === 'queued') return 'Queued';
		if (status === 'running') return 'Running';
		if (status === 'migration_starting') return 'Starting…';
		if (status === 'Migration completed successfully') return 'Finalizing…';
		if (status === 'Migration failed') return 'Failed';
		if (status === 'Migration cancelled') return 'Cancelled';
		const phaseMessages: Record<string, string> = {
			validating_migration_prerequisites: 'Validating prerequisites…',
			replicating_datasets_to_target: 'Replicating datasets…',
			stopping_guest_on_source: 'Stopping guest on source…',
			performing_final_incremental_sync: 'Syncing final changes…',
			starting_guest_on_target: 'Starting guest on target…',
			adjusting_cluster_policies: 'Adjusting cluster policies…',
			cleaning_up_source_guest: 'Cleaning up source…',
			finalizing_migration: 'Finalizing…',
			migration_cancelled: 'Cancelled',
			migration_completed: 'Complete',
			migration_failed: 'Failed'
		};
		if (status in phaseMessages) return phaseMessages[status];
		return status;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-lg p-6" onInteractOutside={(e) => e.preventDefault()}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				<span class="icon-[mdi--swap-horizontal] h-5 w-5 text-primary"></span>
				<span>Migrate {guestName || `${guestTypeLabel()} ${guestId}`}</span>
			</Dialog.Title>
			<Dialog.Description>
				{migrating
					? 'Migration in progress. This dialog can be closed and will continue in the background.'
					: `Select which node to migrate this ${guestTypeLabel()} to.`}
			</Dialog.Description>
		</Dialog.Header>

		{#if !migrating && guestType === 'vm'}
			<div class="rounded-md border bg-muted/10 px-4">
				<Accordion.Root type="single">
					<Accordion.Item value="migration-info" class="border-b-0">
						<Accordion.Trigger
							class="py-2 text-[13px] text-muted-foreground hover:no-underline px-0"
						>
							<span class="icon-[mdi--information-outline] mr-1.5 h-3.5 w-3.5 shrink-0 leading-none"
							></span>
							<span>This is <b>not</b> live migration</span>
						</Accordion.Trigger>
						<Accordion.Content class="pb-3 text-xs text-muted-foreground/80 leading-relaxed">
							<p class="text-justify">
								The VM will be <strong>stopped</strong> on the source node, its datasets transferred
								over the network, and then
								<strong>started</strong> on the target node. There will be a brief downtime during the
								transfer.
							</p>
							<p class="mt-2 text-justify">
								Live migration is under active development.
								<a
									href="https://freebsdfoundation.org/donate/"
									target="_blank"
									class="underline hover:text-primary transition-colors"
								>
									Donate to the FreeBSD Foundation
								</a>
								to help fund work like this.
							</p>
						</Accordion.Content>
					</Accordion.Item>
				</Accordion.Root>
			</div>
		{/if}

		{#if migrating}
			<ScrollArea orientation="vertical" class="h-[40vh] max-h-80">
				<div class="space-y-4 pr-1">
					<div class="space-y-1.5 rounded-md border p-4">
						{#each PHASE_ORDER as phase (phase)}
							{@const status = phaseStatus(phase)}
							{@const detail = phaseDetail(phase)}
							<div class="flex items-center gap-2 text-sm">
								{#if status === 'done'}
									<span class="icon-[mdi--check-circle] h-4 w-4 shrink-0 text-green-500"></span>
									<span class="text-muted-foreground">{PHASE_LABELS[phase]}</span>
								{:else if status === 'active'}
									<span class="icon-[mdi--loading] h-4 w-4 shrink-0 animate-spin text-primary"
									></span>
									<span class="font-medium">{PHASE_LABELS[phase]}</span>
									{#if detail}
										<span class="text-xs text-muted-foreground">({detail})</span>
									{/if}
								{:else}
									<span class="icon-[mdi--circle-outline] h-4 w-4 shrink-0 text-muted-foreground"
									></span>
									<span class="text-muted-foreground/60">{PHASE_LABELS[phase]}</span>
								{/if}
							</div>
						{/each}
					</div>

					{#if parseProgressPercent() >= 0}
						<div class="space-y-1">
							<div class="flex items-center gap-2">
								<Progress value={parseProgressPercent()} max={100} class="flex-1" />
								<span class="text-xs font-medium tabular-nums">{parseProgressPercent()}%</span>
							</div>
						</div>
					{:else if currentPhase === 'initial_replication' || currentPhase === 'final_sync'}
						<div class="space-y-1">
							<div class="flex items-center gap-2">
								<div class="h-2 flex-1 rounded-full bg-primary/20">
									<div class="h-2 animate-pulse rounded-full bg-primary" style="width: 25%"></div>
								</div>
								<span class="text-xs tabular-nums text-muted-foreground">...</span>
							</div>
						</div>
					{/if}

					{#if migrationStatus && parseProgressPercent() < 0 && currentPhase !== 'initial_replication' && currentPhase !== 'final_sync'}
						<p class="text-center text-xs text-muted-foreground">
							{formatStatus(migrationStatus)}
						</p>
					{/if}

					{#if error}
						<Alert variant="destructive">
							<AlertTitle>Error</AlertTitle>
							<AlertDescription>{error}</AlertDescription>
						</Alert>
					{/if}
				</div>
			</ScrollArea>
			<Dialog.Footer>
				<Dialog.Close class={buttonVariants({ variant: 'outline' })}>Close</Dialog.Close>
				{#if currentPhase && currentPhase !== 'finalize' && currentPhase !== 'cleanup_source' && migrationStatus !== 'Migration completed successfully' && migrationStatus !== 'Migration failed' && migrationStatus !== 'Migration cancelled'}
					<Button variant="destructive" onclick={doCancelMigration} size="sm">
						Cancel Migration
					</Button>
				{/if}
			</Dialog.Footer>
		{:else}
			<ScrollArea orientation="vertical" class="h-[40vh] max-h-96">
				<div class="pr-1">
					<div class="divide-y rounded-md border">
						{#each nodes as n (n)}
							{@const isSelected = selectedNodeUuid === n.nodeUUID}
							<button
								type="button"
								class="hover:bg-accent flex w-full items-center gap-3 px-4 py-3 text-left text-sm transition-colors {isSelected
									? 'bg-accent'
									: ''}"
								onclick={() => onTargetSelect(n.nodeUUID)}
							>
								<span class="icon-[mdi--server] h-4 w-4 shrink-0 text-muted-foreground"></span>
								<div class="min-w-0 flex-1">
									<div class="font-medium">{n.hostname || 'Unknown'}</div>
									<div class="text-xs text-muted-foreground">
										{n.cpu} CPU cores &middot; {Math.round(n.memory / 1024 / 1024 / 1024)} GB RAM
									</div>
								</div>
								{#if isSelected}
									<span class="icon-[mdi--check-circle] h-5 w-5 shrink-0 text-primary"></span>
								{:else}
									<span class="h-5 w-5 shrink-0"></span>
								{/if}
							</button>
						{/each}

						{#if nodes.length === 0}
							<div class="px-4 py-8 text-center text-sm text-muted-foreground">
								No other online nodes available in the cluster.
							</div>
						{/if}
					</div>

					<div class="space-y-2 pt-2">
						{#if validating}
							<Alert>
								<AlertDescription class="flex items-center justify-center gap-2 text-base">
									<span class="icon-[mdi--loading] h-5 w-5 animate-spin"></span>
									<span>Validating...</span>
								</AlertDescription>
							</Alert>
						{:else if !validation && !error}
							<div class="h-5"></div>
						{/if}

						{#if validation && !validation.allowed}
							<Alert variant="destructive">
								<AlertTitle>Migration not allowed</AlertTitle>
								<AlertDescription>
									<ul class="list-inside list-disc space-y-0.5">
										{#each validation.reasons as reason (reason)}
											<li>{formatMessage(reason)}</li>
										{/each}
									</ul>
								</AlertDescription>
							</Alert>
						{/if}

						{#if validation && validation.allowed}
							<Alert>
								<AlertTitle class="text-green-600 dark:text-green-400">
									<SpanWithIcon
										icon="icon-[lets-icons--check-fill]"
										size="w-4 h-4"
										gap="gap-1"
										title="Ready to migrate"
									/>
								</AlertTitle>
								<AlertDescription class="text-green-600/80 dark:text-green-400/80">
									Target node is valid for migration.
								</AlertDescription>
							</Alert>
						{/if}

						{#if validation && validation.allowed && validation.warnings && validation.warnings.length > 0}
							<Alert>
								<AlertTitle class="text-yellow-600 dark:text-yellow-400">⚠ Warnings</AlertTitle>
								<AlertDescription>
									<ul
										class="list-inside list-disc space-y-0.5 text-yellow-700 dark:text-yellow-300"
									>
										{#each validation.warnings as warning (warning)}
											<li>{formatMessage(warning)}</li>
										{/each}
									</ul>
								</AlertDescription>
							</Alert>
						{/if}

						{#if error}
							<Alert variant="destructive">
								<AlertTitle>Error</AlertTitle>
								<AlertDescription>{error}</AlertDescription>
							</Alert>
						{/if}
					</div>
				</div>
			</ScrollArea>

			<Dialog.Footer>
				<Button
					onclick={doMigrate}
					disabled={!selectedNodeUuid || !validation?.allowed || loading}
					size="sm"
				>
					{#if loading}
						<SpanWithIcon
							icon="line-md--loading-loop"
							size="h-4 w-4"
							gap="gap-1"
							title="Migrating..."
						/>
					{:else}
						<SpanWithIcon
							icon="icon-[mdi--swap-horizontal]"
							size="h-4 w-4"
							gap="gap-1"
							title="Migrate"
						/>
					{/if}
				</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
