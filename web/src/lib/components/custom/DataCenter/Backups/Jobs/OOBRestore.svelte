<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import {
		getBackupTargetJailMetadata,
		getBackupTargetVMMetadata,
		listBackupTargetDatasets,
		listBackupTargetDatasetSnapshots,
		restoreBackupFromTarget
	} from '$lib/api/cluster/backups';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		BackupGuestRef,
		BackupJailMetadataInfo,
		BackupRestoreGenerationOption,
		BackupSnapshotLineageMarker,
		BackupTarget,
		BackupTargetDatasetInfo,
		BackupVMMetadataInfo,
		RestoreTargetDatasetGroup,
		SnapshotInfo
	} from '$lib/types/cluster/backups';
	import { handleAPIError } from '$lib/utils/http';
	import {
		formatRestoreSnapshotDate,
		inferJailDestinationDataset,
		inferVMDestinationDataset,
		pickRepresentativeDataset,
		snapshotLineageLabel
	} from '$lib/utils/zfs';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		targets: BackupTarget[];
		nodes: ClusterNode[];
		reload: boolean;
	}

	let { open = $bindable(), targets, nodes, reload = $bindable() }: Props = $props();

	let loadingDatasets = $state(false);
	let loadingSnapshots = $state(false);
	let loadingMetadata = $state(false);
	let loadingCluster = $state(false);
	let restoring = $state(false);

	let targetId = $state('');
	let restoreNodeId = $state('');
	let dataset = $state('');
	let selectedGeneration = $state('');
	let snapshot = $state('');
	let destinationDataset = $state('');
	let restoreNetwork = $state(true);

	let datasets = $state<BackupTargetDatasetInfo[]>([]);
	let snapshots = $state<SnapshotInfo[]>([]);
	let jailMetadata = $state<BackupJailMetadataInfo | null>(null);
	let vmMetadata = $state<BackupVMMetadataInfo | null>(null);
	let error = $state('');
	let clusterDetails = $state<ClusterDetails | null>(null);

	function parseGuestFromDatasetPath(datasetPath: string): BackupGuestRef {
		const jailMatch = datasetPath.match(/(?:^|\/)jails\/(\d+)(?:$|[/.])/);
		if (jailMatch) {
			const parsed = Number.parseInt(jailMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'jail', id: parsed };
			}
		}

		const vmMatch = datasetPath.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/);
		if (vmMatch) {
			const parsed = Number.parseInt(vmMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'vm', id: parsed };
			}
		}

		return { kind: 'dataset', id: 0 };
	}

	function snapshotLineageMarker(item: SnapshotInfo): BackupSnapshotLineageMarker {
		const lineage = item.lineage || 'active';
		if (lineage === 'preserved') return 'INT';
		if (lineage === 'active' && !item.outOfBand) return 'CURR';
		return 'OOB';
	}

	function formatSnapshotCount(count: number): string {
		return `${count} ${count === 1 ? 'snap' : 'snaps'}`;
	}

	function extractJobLabel(baseSuffix: string): string {
		const match = baseSuffix.match(/(?:^|\/)(job-[0-9]+|j-[0-9a-z]+)(?:\/|$)/i);
		if (!match) return '';
		return match[1];
	}

	function snapshotGenerationTag(item: SnapshotInfo): string {
		const datasetName =
			(item.dataset && item.dataset.trim()) ||
			(item.name.includes('@') ? item.name.slice(0, item.name.lastIndexOf('@')) : '');
		if (!datasetName) return '';
		const leaf = datasetName.slice(datasetName.lastIndexOf('/') + 1);
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

	function parseSnapshotTimeMs(item: SnapshotInfo): number | null {
		const raw = (item.creation || '').trim();
		if (!raw) return null;
		const ms = Date.parse(raw);
		if (!Number.isFinite(ms) || Number.isNaN(ms)) return null;
		return ms;
	}

	function buildGenerationAliasMap(items: SnapshotInfo[]): Map<string, string> {
		const generationTime = new Map<string, number>();
		for (const item of items) {
			const generation = snapshotGenerationTag(item);
			if (!generation || generation === 'active') continue;

			const generationMs = parseGenerationTimestampMs(generation);
			const snapshotMs = parseSnapshotTimeMs(item);
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

	function snapshotGenerationKey(item: SnapshotInfo): string {
		const lineage = item.lineage || 'active';
		if (lineage === 'active' && !item.outOfBand) {
			return 'active';
		}
		const generation = snapshotGenerationTag(item);
		return generation && generation.trim() !== '' ? generation : 'active';
	}

	function generationLabelFromKey(key: string, aliasByTag: Map<string, string>): string {
		if ((key || '').trim() === '' || key === 'active') return 'Current';
		return aliasByTag.get(key) || key;
	}

	function filterSnapshotsByGeneration(items: SnapshotInfo[], generation: string): SnapshotInfo[] {
		const targetGeneration = (generation || '').trim();
		if (!targetGeneration) return items;
		return items.filter((item) => snapshotGenerationKey(item) === targetGeneration);
	}

	function buildGenerationOptions(
		items: SnapshotInfo[],
		aliasByTag: Map<string, string>
	): BackupRestoreGenerationOption[] {
		if (items.length === 0) return [];

		const groups = new Map<string, { count: number; sortMs: number }>();
		for (const item of items) {
			const key = snapshotGenerationKey(item);
			const generationMs = parseGenerationTimestampMs(key);
			const snapshotMs = parseSnapshotTimeMs(item);
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

	function formatRestoreTargetDatasetLabel(group: RestoreTargetDatasetGroup): string {
		const label = group.jobLabel ? `${group.label} · ${group.jobLabel}` : group.label;
		return `${label} (${formatSnapshotCount(group.totalSnapshots)})`;
	}

	let targetOptions = $derived(
		targets.map((entry) => ({
			value: String(entry.id),
			label: entry.name
		}))
	);

	let nodeNameById = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = node.hostname;
		}
		return out;
	});

	let restoreNodeOptions = $derived.by(() => {
		const detailsNodes = clusterDetails?.nodes || [];
		if (detailsNodes.length > 0) {
			return detailsNodes.map((node) => {
				const hostname = nodeNameById[node.id] || node.id;
				return {
					value: node.id,
					label: node.isLeader ? `${hostname} (Leader)` : hostname
				};
			});
		}

		return nodes.map((node) => ({
			value: node.nodeUUID,
			label: node.hostname
		}));
	});

	let restoreTargetDatasetGroups = $derived.by(() => {
		const grouped = new Map<
			string,
			{
				baseSuffix: string;
				datasets: BackupTargetDatasetInfo[];
				kind: 'dataset' | 'jail' | 'vm';
				jailCtId: number;
				vmRid: number;
				totalSnapshots: number;
			}
		>();

		for (const item of datasets) {
			const baseSuffix = item.baseSuffix || item.suffix || item.name;
			const key = baseSuffix || item.name;
			const existing = grouped.get(key);
			if (!existing) {
				grouped.set(key, {
					baseSuffix: key,
					datasets: [item],
					kind: item.kind || 'dataset',
					jailCtId: item.jailCtId || 0,
					vmRid: item.vmRid || 0,
					totalSnapshots: item.snapshotCount || 0
				});
				continue;
			}

			existing.datasets.push(item);
			existing.totalSnapshots += item.snapshotCount || 0;
			if (existing.kind !== 'jail' && item.kind === 'jail') {
				existing.kind = 'jail';
			}
			if (!existing.jailCtId && item.jailCtId) {
				existing.jailCtId = item.jailCtId;
			}
			if (existing.kind !== 'vm' && item.kind === 'vm') {
				existing.kind = 'vm';
			}
			if (!existing.vmRid && item.vmRid) {
				existing.vmRid = item.vmRid;
			}
		}

		const out: RestoreTargetDatasetGroup[] = [];
		for (const entry of grouped.values()) {
			const representative = pickRepresentativeDataset(entry.datasets);
			if (!representative) continue;

			const displayBase =
				entry.kind === 'jail' && entry.jailCtId > 0
					? `jails/${entry.jailCtId}`
					: entry.kind === 'vm' && entry.vmRid > 0
						? `virtual-machines/${entry.vmRid}`
						: entry.baseSuffix;

			const totalSnapshots =
				entry.kind === 'vm'
					? Math.max(...entry.datasets.map((datasetEntry) => datasetEntry.snapshotCount || 0), 0)
					: entry.totalSnapshots;

			out.push({
				baseSuffix: entry.baseSuffix,
				label: displayBase,
				jobLabel: extractJobLabel(entry.baseSuffix),
				representativeDataset: representative.name,
				kind: entry.kind,
				jailCtId: entry.jailCtId,
				vmRid: entry.vmRid,
				totalSnapshots
			});
		}

		return out.sort((left, right) => left.baseSuffix.localeCompare(right.baseSuffix));
	});

	let visibleRestoreTargetDatasets = $derived.by(() => restoreTargetDatasetGroups);
	let restoreTargetDatasetOptions = $derived(
		visibleRestoreTargetDatasets.map((entry) => ({
			value: entry.representativeDataset,
			label: formatRestoreTargetDatasetLabel(entry)
		}))
	);

	let selectedRestoreTargetDatasetGroup = $derived.by(
		() =>
			visibleRestoreTargetDatasets.find((entry) => entry.representativeDataset === dataset) || null
	);

	let selectedRestoreTargetDatasetKind = $derived.by(
		() => selectedRestoreTargetDatasetGroup?.kind || 'dataset'
	);

	let restoreTargetSupportsNetworkRestore = $derived.by(
		() => selectedRestoreTargetDatasetKind === 'jail' || selectedRestoreTargetDatasetKind === 'vm'
	);

	let generationAliasByTag = $derived.by(() => buildGenerationAliasMap(snapshots));
	let generationOptions = $derived.by(() =>
		buildGenerationOptions(snapshots, generationAliasByTag)
	);
	let visibleSnapshots = $derived.by(() =>
		filterSnapshotsByGeneration(snapshots, selectedGeneration)
	);

	let snapshotOptions = $derived(
		[...visibleSnapshots].reverse().map((item) => {
			const generation = snapshotGenerationTag(item);
			const generationAlias = generationLabelFromKey(generation, generationAliasByTag);
			const marker = snapshotLineageMarker(item);
			const generationLabel =
				marker !== 'CURR' && generation && generation !== 'active' ? ` · ${generationAlias}` : '';
			return {
				value: item.name || item.shortName,
				label: `${formatRestoreSnapshotDate(item)} [${marker}${generationLabel}]`
			};
		})
	);

	let selectedSnapshotInfo = $derived.by(
		() => snapshots.find((entry) => (entry.name || entry.shortName) === snapshot) || null
	);

	let hasOutOfBandSnapshots = $derived.by(() =>
		snapshots.some((entry) => !!entry.outOfBand || (entry.lineage || 'active') !== 'active')
	);

	function nodeLabelByID(nodeId: string): string {
		return nodeNameById[nodeId] || nodeId;
	}

	async function loadRestoreClusterDetails(): Promise<ClusterDetails | null> {
		loadingCluster = true;
		try {
			const details = await getDetails();
			clusterDetails = details;
			return details;
		} catch (e: any) {
			toast.error(e?.message || 'Failed to load cluster details', { position: 'bottom-center' });
			return null;
		} finally {
			loadingCluster = false;
		}
	}

	async function ensureGuestIDPlacementForRestore(
		guestID: number,
		restoreNodeID: string,
		kind: 'jail' | 'vm'
	): Promise<boolean> {
		if (guestID <= 0) return true;

		let details: ClusterDetails;
		try {
			details = clusterDetails || (await getDetails());
			clusterDetails = details;
		} catch (e: any) {
			toast.error(e?.message || 'Failed to validate cluster guest placement', {
				position: 'bottom-center'
			});
			return false;
		}

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

	function resetState(close: boolean = true) {
		if (close) {
			open = false;
		}
		loadingDatasets = false;
		loadingSnapshots = false;
		loadingMetadata = false;
		loadingCluster = false;
		restoring = false;
		targetId = '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		restoreNetwork = true;
		datasets = [];
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;
		error = '';
		clusterDetails = null;
	}

	async function initializeModal() {
		error = '';
		targetId = targetOptions[0]?.value || '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		restoreNetwork = true;
		datasets = [];
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;
		clusterDetails = null;

		const details = await loadRestoreClusterDetails();
		if (details?.nodeId) {
			restoreNodeId = details.nodeId;
		} else {
			restoreNodeId = nodes[0]?.nodeUUID || '';
		}

		if (!targetId) {
			error = 'No backup targets available';
			return;
		}

		await onTargetChange();
	}

	async function onTargetChange() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId) return;

		loadingDatasets = true;
		error = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		datasets = [];
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;

		try {
			const items = await listBackupTargetDatasets(parsedTargetId);
			datasets = items;
			const groupedByBase = new Map<string, BackupTargetDatasetInfo[]>();
			for (const entry of items) {
				const key = entry.baseSuffix || entry.suffix || entry.name;
				if (!groupedByBase.has(key)) {
					groupedByBase.set(key, []);
				}
				groupedByBase.get(key)?.push(entry);
			}

			const representatives: BackupTargetDatasetInfo[] = [];
			for (const group of groupedByBase.values()) {
				const representative = pickRepresentativeDataset(group);
				if (representative) {
					representatives.push(representative);
				}
			}

			representatives.sort((left, right) => {
				const leftKey = left.baseSuffix || left.suffix || left.name;
				const rightKey = right.baseSuffix || right.suffix || right.name;
				return leftKey.localeCompare(rightKey);
			});

			if (representatives.length > 0) {
				dataset = representatives[0].name;
				await onDatasetChange();
			}
		} catch (e: any) {
			error = e?.message || 'Failed to load target datasets';
		} finally {
			loadingDatasets = false;
		}
	}

	async function onDatasetChange() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId || !dataset) return;

		loadingSnapshots = true;
		loadingMetadata = true;
		error = '';
		selectedGeneration = '';
		snapshot = '';
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;

		const selectedTarget = targets.find((entry) => entry.id === parsedTargetId);
		const parsedSourceGuest = parseGuestFromDatasetPath(dataset);
		if (parsedSourceGuest.kind === 'jail') {
			destinationDataset = inferJailDestinationDataset(selectedTarget, dataset);
		} else if (parsedSourceGuest.kind === 'vm') {
			destinationDataset = inferVMDestinationDataset(selectedTarget, dataset);
		} else {
			destinationDataset = '';
		}

		try {
			const [snapshotItems, jailMetadataInfo, vmMetadataInfo] = await Promise.all([
				listBackupTargetDatasetSnapshots(parsedTargetId, dataset),
				getBackupTargetJailMetadata(parsedTargetId, dataset),
				getBackupTargetVMMetadata(parsedTargetId, dataset)
			]);
			snapshots = snapshotItems;
			if (snapshotItems.length > 0) {
				const latest = snapshotItems[snapshotItems.length - 1];
				selectedGeneration = snapshotGenerationKey(latest);
				snapshot = latest.name || latest.shortName;
			} else {
				selectedGeneration = '';
				snapshot = '';
			}

			jailMetadata = jailMetadataInfo;
			vmMetadata = vmMetadataInfo;

			if (jailMetadataInfo?.basePool && jailMetadataInfo?.ctId) {
				destinationDataset = `${jailMetadataInfo.basePool}/sylve/jails/${jailMetadataInfo.ctId}`;
			}
			if (vmMetadataInfo?.rid) {
				const pool = vmMetadataInfo.pools?.[0] || selectedTarget?.backupRoot.split('/')[0] || '';
				if (pool) {
					destinationDataset = `${pool}/sylve/virtual-machines/${vmMetadataInfo.rid}`;
				}
			}
		} catch (e: any) {
			error = e?.message || 'Failed to load dataset details';
		} finally {
			loadingSnapshots = false;
			loadingMetadata = false;
		}
	}

	function onGenerationChange() {
		const visible = filterSnapshotsByGeneration(snapshots, selectedGeneration);
		if (visible.length === 0) {
			snapshot = '';
			return;
		}

		const selectedStillVisible = visible.some(
			(entry) => (entry.name || entry.shortName) === snapshot
		);
		if (selectedStillVisible) return;

		const latest = visible[visible.length - 1];
		snapshot = latest.name || latest.shortName;
	}

	async function triggerRestoreFromTarget() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId || !dataset || !snapshot) return;
		if (!destinationDataset.trim()) {
			toast.error('Destination dataset is required', { position: 'bottom-center' });
			return;
		}
		if (!destinationDataset.trim().includes('/')) {
			toast.error('Destination dataset must be fully qualified (for example: zroot/yyy)', {
				position: 'bottom-center'
			});
			return;
		}
		if (!restoreNodeId.trim()) {
			toast.error('Restore node is required', { position: 'bottom-center' });
			return;
		}

		restoring = true;
		try {
			const destinationGuest = parseGuestFromDatasetPath(destinationDataset);
			const metadataGuest = jailMetadata?.ctId || vmMetadata?.rid || 0;
			const guestID = destinationGuest.id || metadataGuest;
			const guestKind: 'jail' | 'vm' =
				destinationGuest.kind === 'vm' || (destinationGuest.kind === 'dataset' && !!vmMetadata?.rid)
					? 'vm'
					: 'jail';

			if (guestID > 0) {
				const allowed = await ensureGuestIDPlacementForRestore(
					guestID,
					restoreNodeId.trim(),
					guestKind
				);
				if (!allowed) {
					restoring = false;
					return;
				}
			}

			const response = await restoreBackupFromTarget(parsedTargetId, {
				remoteDataset: dataset,
				snapshot,
				destinationDataset: destinationDataset.trim(),
				restoreNodeId: restoreNodeId.trim(),
				restoreNetwork
			});

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

	watch([() => open, () => targetOptions.length], ([isOpen]) => {
		if (!isOpen) {
			resetState(false);
			return;
		}
		void initializeModal();
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--database-sync-outline] h-5 w-5"></span>
					<span>Restore From Target Dataset</span>
				</div>

				<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Close'}</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
				<SimpleSelect
					label="Target"
					placeholder="Select target"
					options={targetOptions}
					bind:value={targetId}
					onChange={onTargetChange}
				/>

				<SimpleSelect
					label="Restore On Node"
					placeholder={loadingCluster
						? 'Loading nodes...'
						: restoreNodeOptions.length === 0
							? 'No cluster nodes found'
							: 'Select restore node'}
					options={restoreNodeOptions}
					bind:value={restoreNodeId}
					onChange={() => {}}
					disabled={loadingCluster || restoreNodeOptions.length === 0}
				/>

				<SimpleSelect
					label="Dataset on Target"
					placeholder={loadingDatasets
						? 'Loading datasets...'
						: visibleRestoreTargetDatasets.length === 0
							? 'No restorable datasets found'
							: 'Select dataset'}
					options={restoreTargetDatasetOptions}
					bind:value={dataset}
					onChange={onDatasetChange}
					disabled={loadingDatasets || visibleRestoreTargetDatasets.length === 0}
				/>
			</div>

			{#if hasOutOfBandSnapshots}
				<div class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-blue-700">
					This backup set includes non-active lineages. Snapshot markers show lineage as
					<code class="rounded bg-background px-1">CURR</code>,
					<code class="rounded bg-background px-1">OOB</code>, and
					<code class="rounded bg-background px-1">INT</code>.
				</div>
			{/if}

			<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
				<SimpleSelect
					label="Generation"
					placeholder={loadingSnapshots
						? 'Loading generations...'
						: generationOptions.length === 0
							? 'No generations found'
							: 'Select generation'}
					options={generationOptions}
					bind:value={selectedGeneration}
					onChange={onGenerationChange}
					disabled={loadingSnapshots || generationOptions.length === 0}
				/>

				<SimpleSelect
					label="Snapshot"
					placeholder={loadingSnapshots
						? 'Loading snapshots...'
						: visibleSnapshots.length === 0
							? 'No snapshots found'
							: 'Select snapshot'}
					options={snapshotOptions}
					bind:value={snapshot}
					onChange={() => {}}
					disabled={loadingSnapshots || visibleSnapshots.length === 0}
				/>

				<CustomValueInput
					label="Destination Dataset"
					placeholder={selectedRestoreTargetDatasetKind === 'vm'
						? 'zroot/sylve/virtual-machines/104'
						: selectedRestoreTargetDatasetKind === 'jail'
							? 'zroot/sylve/jails/105'
							: 'pool/path'}
					bind:value={destinationDataset}
					classes="space-y-1"
				/>
			</div>

			{#if restoreTargetSupportsNetworkRestore}
				<CustomCheckbox
					label={selectedRestoreTargetDatasetKind === 'vm'
						? 'Restore VM Network Config'
						: 'Restore Jail Network Config'}
					bind:checked={restoreNetwork}
					classes="flex items-center gap-2"
				/>
			{/if}

			{#if jailMetadata}
				<div class="rounded-md border bg-muted/40 p-3 text-sm">
					<p class="font-medium">Detected Jail Metadata</p>
					<div class="mt-2 grid grid-cols-1 gap-1 text-muted-foreground md:grid-cols-3">
						<div>
							Name: <code class="rounded bg-background px-1">{jailMetadata.name || '-'}</code>
						</div>
						<div>
							CT ID: <code class="rounded bg-background px-1">{jailMetadata.ctId}</code>
						</div>
						<div>
							Base Pool: <code class="rounded bg-background px-1"
								>{jailMetadata.basePool || '-'}</code
							>
						</div>
					</div>
				</div>
			{/if}

			{#if vmMetadata}
				<div class="rounded-md border bg-muted/40 p-3 text-sm">
					<p class="font-medium">Detected VM Metadata</p>
					<div class="mt-2 grid grid-cols-1 gap-1 text-muted-foreground md:grid-cols-3">
						<div>
							Name: <code class="rounded bg-background px-1">{vmMetadata.name || '-'}</code>
						</div>
						<div>
							RID: <code class="rounded bg-background px-1">{vmMetadata.rid}</code>
						</div>
						<div>
							Pools:
							<code class="rounded bg-background px-1"
								>{vmMetadata.pools?.length ? vmMetadata.pools.join(', ') : '-'}</code
							>
						</div>
					</div>
				</div>
			{/if}

			{#if error}
				<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
					{error}
				</div>
			{/if}

			<div class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-sm">
				<div class="flex items-center gap-1 font-medium text-yellow-600 dark:text-yellow-400">
					<span class="icon-[mdi--alert] h-4 w-4"></span>
					<span>Restore Warning</span>
				</div>
				<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
					<li>The destination dataset will be replaced if it already exists.</li>
					{#if selectedSnapshotInfo}
						<li>
							Selected backup date:
							<code class="rounded bg-background px-1"
								>{formatRestoreSnapshotDate(selectedSnapshotInfo)}</code
							>
						</li>
						{#if selectedSnapshotInfo.outOfBand || selectedSnapshotInfo.lineage !== 'active'}
							<li>
								Selected snapshot lineage:
								<code class="rounded bg-background px-1"
									>{snapshotLineageLabel(selectedSnapshotInfo)}</code
								>
							</li>
						{/if}
					{/if}
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button
				onclick={triggerRestoreFromTarget}
				disabled={restoring ||
					loadingDatasets ||
					loadingSnapshots ||
					loadingCluster ||
					!targetId ||
					!restoreNodeId ||
					!dataset ||
					!snapshot ||
					!destinationDataset.trim()}
				variant="destructive"
			>
				{#if restoring}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						<span>Restoring...</span>
					</div>
				{:else}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--database-sync-outline] h-4 w-4"></span>
						<span>Restore</span>
					</div>
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
