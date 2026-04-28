<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import {
		getBackupEvents,
		listBackupJobSnapshots,
		restoreBackupJob
	} from '$lib/api/cluster/backups';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type { BackupGuestRef, BackupJob, SnapshotInfo } from '$lib/types/cluster/backups';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import {
		buildGenerationAliasMap,
		buildGenerationOptions,
		filterSnapshotsByGeneration,
		formatRestoreSnapshotDate,
		generationLabelFromKey,
		snapshotGenerationKey,
		snapshotGenerationTag,
		snapshotLineageLabel,
		snapshotLineageMarker
	} from '$lib/utils/zfs';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		selectedJob: BackupJob | null;
		nodes: ClusterNode[];
		reload: boolean;
	}

	let { open = $bindable(), selectedJob, nodes, reload = $bindable() }: Props = $props();

	let loading = $state(false);
	let restoring = $state(false);
	let jobRunning = $state(false);
	let snapshots = $state<SnapshotInfo[]>([]);
	let selectedGeneration = $state('');
	let selectedSnapshot = $state('');
	let error = $state('');
	let clusterDetails = $state<ClusterDetails | null>(null);

	let nodeNameById = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = node.hostname;
		}
		return out;
	});

	function parseGuestFromDatasetPath(dataset: string): BackupGuestRef {
		const jailMatch = dataset.match(/(?:^|\/)jails\/(\d+)(?:$|[/.])/);
		if (jailMatch) {
			const parsed = Number.parseInt(jailMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) return { kind: 'jail', id: parsed };
		}
		const vmMatch = dataset.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/);
		if (vmMatch) {
			const parsed = Number.parseInt(vmMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) return { kind: 'vm', id: parsed };
		}
		return { kind: 'dataset', id: 0 };
	}

	function snapshotLineageIcon(snapshot: SnapshotInfo): {
		icon: string;
		className: string;
		title: string;
	} {
		const marker = snapshotLineageMarker(snapshot);
		if (marker === 'CURR') {
			return {
				icon: 'icon-[mdi--check-circle-outline]',
				className: 'text-green-600',
				title: 'Current lineage'
			};
		}
		if (marker === 'INT') {
			return {
				icon: 'icon-[mdi--archive-outline]',
				className: 'text-orange-600',
				title: 'System-preserved lineage'
			};
		}
		return {
			icon: 'icon-[mdi--source-branch]',
			className: 'text-blue-600',
			title: 'Out-of-band lineage'
		};
	}

	function nodeLabelByID(nodeId: string): string {
		return nodeNameById[nodeId] || nodeId;
	}

	async function loadClusterDetails(): Promise<ClusterDetails | null> {
		try {
			const details = await getDetails();
			if (isAPIResponse(details)) {
				return null;
			}

			clusterDetails = details;
			return details;
		} catch (e: unknown) {
			toast.error((e as { message?: string })?.message || 'Failed to load cluster details', {
				position: 'bottom-center'
			});
			return null;
		}
	}

	async function ensureGuestIDPlacementForRestore(
		guestID: number,
		restoreNodeID: string,
		kind: 'jail' | 'vm'
	): Promise<boolean> {
		if (guestID <= 0) return true;

		const details = clusterDetails || (await loadClusterDetails());
		if (!details) return false;

		const registeredOn = details.nodes.filter((node) => (node.guestIDs || []).includes(guestID));
		if (registeredOn.length === 0) return true;

		const conflicts = registeredOn.filter((node) => node.id !== restoreNodeID);
		if (conflicts.length === 0 && registeredOn.length === 1) {
			return true;
		}

		const conflictLabels =
			conflicts.length > 0
				? conflicts.map((node) => nodeLabelByID(node.id)).join(', ')
				: registeredOn.map((node) => nodeLabelByID(node.id)).join(', ');

		const guestLabel = kind === 'vm' ? 'VM' : 'Jail';
		toast.error(`${guestLabel} ${guestID} already exists on ${conflictLabels}.`, {
			position: 'bottom-center'
		});
		return false;
	}

	async function loadSnapshots() {
		if (!selectedJob) return;

		loading = true;
		jobRunning = false;
		snapshots = [];
		selectedGeneration = '';
		selectedSnapshot = '';
		error = '';
		restoring = false;
		clusterDetails = null;

		try {
			const [items, events] = await Promise.all([
				listBackupJobSnapshots(selectedJob.id),
				getBackupEvents(5, selectedJob.id)
			]);
			jobRunning =
				events.some((e) => e.status === 'running' && !e.completedAt) ||
				selectedJob.lastStatus === 'running';
			if (!jobRunning) {
				snapshots = items;
				if (items.length > 0) {
					const latest = items[items.length - 1];
					selectedGeneration = snapshotGenerationKey(latest);
					selectedSnapshot = latest.name;
				}
			}
		} catch (e: unknown) {
			error = (e as { message?: string })?.message || 'Failed to load snapshots';
		} finally {
			loading = false;
		}
	}

	function onGenerationChange() {
		const visible = filterSnapshotsByGeneration(snapshots, selectedGeneration);
		if (visible.length === 0) {
			selectedSnapshot = '';
			return;
		}

		const selectedStillVisible = visible.some((snapshot) => snapshot.name === selectedSnapshot);
		if (selectedStillVisible) return;

		const latest = visible[visible.length - 1];
		selectedSnapshot = latest.name;
	}

	async function triggerRestore() {
		if (!selectedJob || !selectedSnapshot) return;
		restoring = true;

		try {
			if (selectedJob.mode === 'jail' || selectedJob.mode === 'vm') {
				const primaryDataset =
					selectedJob.mode === 'jail'
						? selectedJob.jailRootDataset || selectedJob.sourceDataset || ''
						: selectedJob.sourceDataset || selectedJob.jailRootDataset || '';
				const parsedGuest = parseGuestFromDatasetPath(primaryDataset);
				const guestID = parsedGuest.id;

				if (guestID > 0) {
					const details = clusterDetails || (await loadClusterDetails());
					const restoreNodeID =
						(selectedJob.runnerNodeId || '').trim() ||
						(details?.leaderId || '').trim() ||
						(details?.nodeId || '').trim();

					if (
						parsedGuest.kind !== 'dataset' &&
						!(await ensureGuestIDPlacementForRestore(guestID, restoreNodeID, parsedGuest.kind))
					) {
						restoring = false;
						return;
					}
				}
			}

			const response = await restoreBackupJob(selectedJob.id, selectedSnapshot);
			if (response.status === 'success') {
				toast.success('Restore job started - check events for progress', {
					position: 'bottom-center'
				});
				open = false;
				reload = true;
				return;
			}
			handleAPIError(response);
			toast.error('Failed to start restore', { position: 'bottom-center' });
		} catch (e: unknown) {
			toast.error((e as { message?: string })?.message || 'Failed to start restore', {
				position: 'bottom-center'
			});
		} finally {
			restoring = false;
		}
	}

	let generationAliasByTag = $derived.by(() => buildGenerationAliasMap(snapshots));
	let generationOptions = $derived.by(() =>
		buildGenerationOptions(snapshots, generationAliasByTag)
	);
	let visibleSnapshots = $derived.by(() =>
		filterSnapshotsByGeneration(snapshots, selectedGeneration)
	);

	let selectedSnapshotDate = $derived.by(() => {
		if (!selectedSnapshot) return '';
		const selected = snapshots.find((snapshot) => snapshot.name === selectedSnapshot);
		if (!selected) return '';
		return formatRestoreSnapshotDate(selected);
	});

	let selectedSnapshotInfo = $derived.by(
		() => snapshots.find((snapshot) => snapshot.name === selectedSnapshot) || null
	);

	let hasOutOfBandSnapshots = $derived.by(() =>
		snapshots.some(
			(snapshot) => !!snapshot.outOfBand || (snapshot.lineage || 'active') !== 'active'
		)
	);

	watch([() => open, () => selectedJob?.id || 0], ([isOpen]) => {
		if (!isOpen) return;
		void loadSnapshots();
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-full max-w-xl! overflow-hidden p-5" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--backup-restore]"
					size="h-5 w-5"
					gap="gap-2"
					title="Restore from Backup"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			{#if jobRunning}
				<div
					class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-4 text-center text-sm text-yellow-700 dark:text-yellow-400"
				>
					A backup is currently in progress. Restore is unavailable until it completes.
				</div>
			{:else if loading}
				<div class="flex items-center justify-center py-8">
					<span class="icon-[mdi--loading] h-6 w-6 animate-spin text-muted-foreground"></span>
					<span class="ml-2 text-muted-foreground">Loading snapshots from remote target...</span>
				</div>
			{:else if error}
				<div class="rounded-md border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-500">
					<p class="font-medium">Failed to load snapshots</p>
					<p class="mt-1">{error}</p>
				</div>
			{:else if snapshots.length === 0}
				<div class="rounded-md bg-muted p-4 text-center text-sm text-muted-foreground">
					No snapshots found on the backup target. Run a backup first.
				</div>
			{:else}
				<SimpleSelect
					label="Generation"
					placeholder="Select generation"
					options={generationOptions}
					bind:value={selectedGeneration}
					onChange={onGenerationChange}
					disabled={generationOptions.length === 0}
				/>

				{#if hasOutOfBandSnapshots}
					<div
						class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-blue-700"
					>
						Some backups are from out-of-band lineages. Regular prune count applies to the current
						lineage only.
					</div>
				{/if}

				{#if visibleSnapshots.length === 0}
					<div class="rounded-md bg-muted p-4 text-center text-sm text-muted-foreground">
						No snapshots found in the selected generation.
					</div>
				{:else}
					<div class="max-h-72 overflow-auto rounded-md border">
						<table class="w-full text-sm">
							<thead class="sticky top-0 bg-muted">
								<tr>
									<th class="w-8 p-2"></th>
									<th class="p-2 text-left font-medium">Backup Date</th>
									<th class="p-2 text-right font-medium">Used</th>
									<th class="p-2 text-right font-medium">Refer</th>
								</tr>
							</thead>
							<tbody>
								{#each [...visibleSnapshots].reverse() as snapshot (snapshot.name)}
									{@const generation = snapshotGenerationTag(snapshot)}
									{@const generationAlias = generationLabelFromKey(
										generation,
										generationAliasByTag
									)}
									<tr
										class="cursor-pointer border-t transition-colors hover:bg-accent {selectedSnapshot ===
										snapshot.name
											? 'bg-accent'
											: ''}"
										onclick={() => (selectedSnapshot = snapshot.name)}
										title={snapshot.name}
									>
										<td class="p-2 text-center">
											{#if selectedSnapshot === snapshot.name}
												<span class="icon-[mdi--radiobox-marked] h-4 w-4 text-primary"></span>
											{:else}
												<span class="icon-[mdi--radiobox-blank] h-4 w-4 text-muted-foreground"
												></span>
											{/if}
										</td>
										<td class="p-2 text-xs text-muted-foreground"
											><span class="inline-flex items-center gap-1">
												{#if snapshotLineageMarker(snapshot) !== 'CURR'}
													{@const lineageIcon = snapshotLineageIcon(snapshot)}
													<span
														class={`${lineageIcon.icon} h-3.5 w-3.5 ${lineageIcon.className}`}
														title={`${snapshotLineageLabel(snapshot)} (${snapshotLineageMarker(snapshot)})`}
													></span>
												{/if}
												<span>{formatRestoreSnapshotDate(snapshot)}</span>
												{#if snapshotLineageMarker(snapshot) !== 'CURR' && generation && generation !== 'active'}
													<code class="rounded bg-background px-1 text-[10px] text-foreground"
														>{generationAlias}</code
													>
												{/if}
											</span></td
										>
										<td class="p-2 text-right text-xs text-muted-foreground"
											>{formatBytesBinary(snapshot.used)}</td
										>
										<td class="p-2 text-right text-xs text-muted-foreground"
											>{formatBytesBinary(snapshot.refer)}</td
										>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}

				<div class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-sm">
					<div class="flex items-center gap-1 font-medium text-yellow-600 dark:text-yellow-400">
						<span class="icon-[mdi--alert] h-4 w-4 text-yellow-600 dark:text-yellow-400"></span>
						<span>Restore Warning</span>
					</div>
					<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
						<li>The current dataset is replaced in place with the selected restore point</li>
						<li>
							Data from <code class="rounded bg-background px-1"
								>{selectedSnapshotDate || selectedSnapshot}</code
							> will be restored
						</li>
						{#if selectedSnapshotInfo && (selectedSnapshotInfo.outOfBand || selectedSnapshotInfo.lineage !== 'active')}
							<li>
								This snapshot is from <code class="rounded bg-background px-1"
									>{snapshotLineageLabel(selectedSnapshotInfo)}</code
								> and may not be counted by active-lineage prune.
							</li>
						{/if}
						<li>No deletion on target, all snapshots remain available</li>
					</ul>
				</div>
			{/if}
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button
				onclick={triggerRestore}
				disabled={!selectedSnapshot || restoring || loading || jobRunning}
				variant="destructive"
			>
				{#if restoring}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						<span>Restoring</span>
					</div>
				{:else}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--backup-restore] h-4 w-4"></span>
						<span>Restore</span>
					</div>
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
