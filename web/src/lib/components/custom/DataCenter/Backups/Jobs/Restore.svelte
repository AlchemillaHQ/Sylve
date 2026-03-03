<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import { listBackupJobSnapshots, restoreBackupJob } from '$lib/api/cluster/backups';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		BackupGuestRef,
		BackupJob,
		BackupRestoreGenerationOption,
		BackupSnapshotLineageMarker,
		SnapshotInfo
	} from '$lib/types/cluster/backups';
	import { handleAPIError } from '$lib/utils/http';
	import { humanFormatBytes } from '$lib/utils/string';
	import { snapshotLineageLabel, formatRestoreSnapshotDate } from '$lib/utils/zfs';
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
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'jail', id: parsed };
			}
		}

		const vmMatch = dataset.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/);
		if (vmMatch) {
			const parsed = Number.parseInt(vmMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'vm', id: parsed };
			}
		}

		return { kind: 'dataset', id: 0 };
	}

	function snapshotLineageMarker(snapshot: SnapshotInfo): BackupSnapshotLineageMarker {
		const lineage = snapshot.lineage || 'active';
		if (lineage === 'preserved') return 'INT';
		if (lineage === 'active' && !snapshot.outOfBand) return 'CURR';
		return 'OOB';
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

	function snapshotGenerationTag(snapshot: SnapshotInfo): string {
		const dataset =
			(snapshot.dataset && snapshot.dataset.trim()) ||
			(snapshot.name.includes('@') ? snapshot.name.slice(0, snapshot.name.lastIndexOf('@')) : '');
		if (!dataset) return '';
		const leaf = dataset.slice(dataset.lastIndexOf('/') + 1);
		if (!leaf) return '';
		if (leaf === 'active') return 'active';

		const marker = leaf.match(/(?:^|_)((?:bk|zelta)_[0-9a-z._-]+|gen-[0-9a-z._-]+)$/i);
		if (marker) return marker[1];
		return leaf;
	}

	function parseGenerationTimestampMs(tag: string): number | null {
		const trimmed = (tag || '').trim().toLowerCase();
		if (!trimmed || trimmed === 'active') return null;

		const token = trimmed.startsWith('gen-')
			? trimmed.slice(4)
			: trimmed.startsWith('bk_')
				? trimmed.slice(3)
				: trimmed.startsWith('zelta_')
					? trimmed.slice(6)
					: '';
		if (!token) return null;

		const parts = token.split('_');
		const candidate = parts.length > 1 ? parts[parts.length - 1] : token;
		if (!candidate) return null;

		const parsed = Number.parseInt(candidate, 36);
		if (!Number.isFinite(parsed) || Number.isNaN(parsed) || parsed <= 0) return null;
		return parsed;
	}

	function parseSnapshotTimeMs(snapshot: SnapshotInfo): number | null {
		const raw = (snapshot.creation || '').trim();
		if (!raw) return null;
		const ms = Date.parse(raw);
		if (!Number.isFinite(ms) || Number.isNaN(ms)) return null;
		return ms;
	}

	function buildGenerationAliasMap(items: SnapshotInfo[]): Map<string, string> {
		const generationTime = new Map<string, number>();
		for (const snapshot of items) {
			const generation = snapshotGenerationTag(snapshot);
			if (!generation || generation === 'active') continue;

			const generationMs = parseGenerationTimestampMs(generation);
			const snapshotMs = parseSnapshotTimeMs(snapshot);
			const inferredMs = generationMs ?? snapshotMs ?? Number.MAX_SAFE_INTEGER;

			const existing = generationTime.get(generation);
			if (existing === undefined || inferredMs < existing) {
				generationTime.set(generation, inferredMs);
			}
		}

		const ordered = [...generationTime.entries()].sort((left, right) => {
			if (left[1] !== right[1]) return left[1] - right[1];
			return left[0].localeCompare(right[0]);
		});

		const aliases = new Map<string, string>();
		for (let index = 0; index < ordered.length; index++) {
			aliases.set(ordered[index][0], `gen-${index + 1}`);
		}

		return aliases;
	}

	function snapshotGenerationKey(snapshot: SnapshotInfo): string {
		const lineage = snapshot.lineage || 'active';
		if (lineage === 'active' && !snapshot.outOfBand) {
			return 'active';
		}
		const generation = snapshotGenerationTag(snapshot);
		return generation && generation.trim() !== '' ? generation : 'active';
	}

	function generationLabelFromKey(key: string, aliasByTag: Map<string, string>): string {
		if ((key || '').trim() === '' || key === 'active') return 'Current';
		return aliasByTag.get(key) || key;
	}

	function filterSnapshotsByGeneration(items: SnapshotInfo[], generation: string): SnapshotInfo[] {
		const target = (generation || '').trim();
		if (!target) return items;
		return items.filter((snapshot) => snapshotGenerationKey(snapshot) === target);
	}

	function buildGenerationOptions(
		items: SnapshotInfo[],
		aliasByTag: Map<string, string>
	): BackupRestoreGenerationOption[] {
		if (items.length === 0) return [];

		const groups = new Map<string, { count: number; sortMs: number }>();
		for (const snapshot of items) {
			const key = snapshotGenerationKey(snapshot);
			const generationMs = parseGenerationTimestampMs(key);
			const snapshotMs = parseSnapshotTimeMs(snapshot);
			const inferredMs = generationMs ?? snapshotMs ?? Number.MAX_SAFE_INTEGER;

			const existing = groups.get(key);
			if (!existing) {
				groups.set(key, { count: 1, sortMs: inferredMs });
				continue;
			}

			existing.count += 1;
			if (inferredMs < existing.sortMs) {
				existing.sortMs = inferredMs;
			}
		}

		const ordered = [...groups.entries()].sort((left, right) => {
			const leftKey = left[0];
			const rightKey = right[0];
			if (leftKey === 'active' && rightKey !== 'active') return -1;
			if (rightKey === 'active' && leftKey !== 'active') return 1;
			if (left[1].sortMs !== right[1].sortMs) return left[1].sortMs - right[1].sortMs;
			return leftKey.localeCompare(rightKey);
		});

		return ordered.map(([key, meta]) => ({
			value: key,
			label: `${generationLabelFromKey(key, aliasByTag)} (${meta.count})`
		}));
	}

	function nodeLabelByID(nodeId: string): string {
		return nodeNameById[nodeId] || nodeId;
	}

	async function loadClusterDetails(): Promise<ClusterDetails | null> {
		try {
			const details = await getDetails();
			clusterDetails = details;
			return details;
		} catch (e: any) {
			toast.error(e?.message || 'Failed to load cluster details', { position: 'bottom-center' });
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
		snapshots = [];
		selectedGeneration = '';
		selectedSnapshot = '';
		error = '';
		restoring = false;
		clusterDetails = null;

		try {
			const items = await listBackupJobSnapshots(selectedJob.id);
			snapshots = items;
			if (items.length > 0) {
				const latest = items[items.length - 1];
				selectedGeneration = snapshotGenerationKey(latest);
				selectedSnapshot = latest.name;
			}
		} catch (e: any) {
			error = e?.message || 'Failed to load snapshots';
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
		} catch (e: any) {
			toast.error(e?.message || 'Failed to start restore', { position: 'bottom-center' });
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
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--backup-restore] h-5 w-5"></span>
					<span>Restore from Backup</span>
				</div>

				<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Close'}</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			{#if loading}
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
								{#each [...visibleSnapshots].reverse() as snapshot}
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
											>{humanFormatBytes(snapshot.used)}</td
										>
										<td class="p-2 text-right text-xs text-muted-foreground"
											>{humanFormatBytes(snapshot.refer)}</td
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
				disabled={!selectedSnapshot || restoring || loading}
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
