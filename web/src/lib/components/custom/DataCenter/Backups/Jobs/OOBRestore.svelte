<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import {
		getTargetRunningJobIds,
		listBackupTargetDatasets,
		listBackupTargetDatasetSnapshots,
		restoreBackupFromTarget
	} from '$lib/api/cluster/backups';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		BackupGuestKind,
		BackupJob,
		BackupTarget,
		BackupTargetDatasetInfo,
		RestoreTargetDatasetGroup,
		SnapshotInfo
	} from '$lib/types/cluster/backups';
	import { getPools } from '$lib/api/zfs/pool';
	import { sleep } from '$lib/utils';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { getNextGuestId } from '$lib/utils/vm/vm';
	import {
		buildGenerationAliasMap,
		buildGenerationOptions,
		filterSnapshotsByGeneration,
		formatRestoreSnapshotDate,
		generationLabelFromKey,
		pickRepresentativeDataset,
		snapshotGenerationKey,
		snapshotGenerationTag,
		snapshotLineageLabel,
		snapshotLineageMarker
	} from '$lib/utils/zfs';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';

	interface Props {
		open: boolean;
		targets: BackupTarget[];
		jobs: BackupJob[];
		nodes: ClusterNode[];
		reload: boolean;
	}

	let { open = $bindable(), targets, jobs, nodes, reload = $bindable() }: Props = $props();

	const kindIcons: Record<BackupGuestKind, string> = {
		jail: 'icon-[hugeicons--prison]',
		vm: 'icon-[material-symbols--monitor-outline-rounded]',
		dataset: 'icon-[mdi--database-outline]'
	};
	const kindOrder: Record<BackupGuestKind, number> = { jail: 0, vm: 1, dataset: 2 };

	let loadingDatasets = $state(false);
	let loadingSnapshots = $state(false);
	let loadingCluster = $state(false);
	let restoring = $state(false);
	let runningJobIds = new SvelteSet<number>();
	let runningJobStatusAvailable = $state(false);
	let checkingRunningJobStatus = $state(false);
	let targetLoadRevision = 0;

	let targetId = $state('');
	let restoreNodeId = $state('');
	let dataset = $state('');
	let selectedGeneration = $state('');
	let snapshot = $state('');
	let destinationDataset = $state('');
	let destinationPool = $state('');
	let destinationGuestId = $state<string | number>('');
	let restorePools = $state<string[]>([]);
	let loadingPools = $state(false);
	let poolLoadRevision = 0;
	let restoreNetwork = $state(true);
	let encryptionKey = $state('');
	let restoreOperationId = $state('');
	let restoreOperationRequestKey = $state('');

	let datasets = $state<BackupTargetDatasetInfo[]>([]);
	let snapshots = $state<SnapshotInfo[]>([]);
	let nextSnapshotCursor = $state('');
	let hasMoreSnapshots = $state(false);
	let loadingOlderSnapshots = $state(false);
	let olderSnapshotsError = $state('');
	let snapshotLoadRevision = 0;
	let error = $state('');
	let clusterDetails = $state<ClusterDetails | null>(null);

	function formatSnapshotCount(count: number): string {
		return `${count} ${count === 1 ? 'snap' : 'snaps'}`;
	}

	function extractJobLabel(baseSuffix: string): string {
		const match = baseSuffix.match(/(?:^|\/)(job-[0-9]+|j-[0-9a-z]+)(?:\/|$)/i);
		if (!match) return '';
		return match[1];
	}

	function jobIdFromLabel(label: string): number {
		const normalized = label.trim().toLowerCase();
		let parsed = 0;
		if (/^j-[0-9a-z]+$/.test(normalized)) {
			parsed = Number.parseInt(normalized.slice(2), 36);
		} else if (/^job-[0-9]+$/.test(normalized)) {
			parsed = Number.parseInt(normalized.slice(4), 10);
		}
		return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 0;
	}

	// Names come from the job that made the backup: the guest's name, or the
	// source dataset.
	function restoreTargetDatasetLabel(
		kind: BackupGuestKind,
		guestId: number,
		baseSuffix: string,
		job: BackupJob | undefined
	): string {
		if (kind !== 'dataset' && guestId > 0) {
			return `${job?.friendlySrc || (kind === 'jail' ? 'Jail' : 'VM')} · ${guestId}`;
		}
		return (
			job?.friendlySrc ||
			job?.sourceDataset ||
			baseSuffix.replace(/\/(?:job-[0-9]+|j-[0-9a-z]+)(?:\/.*)?$/i, '')
		);
	}

	let targetOptions = $derived(
		targets.map((entry) => ({
			value: String(entry.id),
			label: entry.name
		}))
	);

	let nodeNameById = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) out[node.nodeUUID] = node.hostname;
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
		return nodes.map((node) => ({ value: node.nodeUUID, label: node.hostname }));
	});

	let restoreTargetDatasetGroups = $derived.by(() => {
		const grouped = new SvelteMap<
			string,
			{
				baseSuffix: string;
				datasets: BackupTargetDatasetInfo[];
				kind: 'dataset' | 'jail' | 'vm';
				jailCtId: number;
				vmRid: number;
				totalSnapshots: number;
				snapshotCountKnown: boolean;
				encrypted: boolean;
			}
		>();

		for (const item of datasets) {
			const key = item.baseSuffix || item.suffix || item.name;
			const existing = grouped.get(key);
			if (!existing) {
				grouped.set(key, {
					baseSuffix: key,
					datasets: [item],
					kind: item.kind || 'dataset',
					jailCtId: item.jailCtId || 0,
					vmRid: item.vmRid || 0,
					totalSnapshots: item.snapshotCount || 0,
					snapshotCountKnown: item.snapshotCountKnown,
					encrypted: item.encrypted
				});
				continue;
			}
			existing.datasets.push(item);
			existing.totalSnapshots += item.snapshotCount || 0;
			existing.snapshotCountKnown &&= item.snapshotCountKnown;
			existing.encrypted ||= item.encrypted;
			if (existing.kind !== 'jail' && item.kind === 'jail') existing.kind = 'jail';
			if (!existing.jailCtId && item.jailCtId) existing.jailCtId = item.jailCtId;
			if (existing.kind !== 'vm' && item.kind === 'vm') existing.kind = 'vm';
			if (!existing.vmRid && item.vmRid) existing.vmRid = item.vmRid;
		}

		const out: RestoreTargetDatasetGroup[] = [];
		for (const entry of grouped.values()) {
			const representative = pickRepresentativeDataset(entry.datasets);
			if (!representative) continue;

			const jobLabel = extractJobLabel(entry.baseSuffix);
			const jobId = jobIdFromLabel(jobLabel);
			const job = jobs.find((candidate) => candidate.id === jobId);
			const guestId = entry.kind === 'jail' ? entry.jailCtId : entry.vmRid;

			const totalSnapshots =
				entry.kind === 'vm'
					? Math.max(...entry.datasets.map((d) => d.snapshotCount || 0), 0)
					: entry.totalSnapshots;

			out.push({
				baseSuffix: entry.baseSuffix,
				label: restoreTargetDatasetLabel(entry.kind, guestId, entry.baseSuffix, job),
				jobLabel,
				representativeDataset: representative.name,
				kind: entry.kind,
				jailCtId: entry.jailCtId,
				vmRid: entry.vmRid,
				totalSnapshots,
				snapshotCountKnown: entry.snapshotCountKnown,
				encrypted: entry.encrypted
			});
		}

		return out.sort(
			(left, right) =>
				kindOrder[left.kind] - kindOrder[right.kind] || left.label.localeCompare(right.label)
		);
	});

	let restoreTargetDatasetOptions = $derived(
		restoreTargetDatasetGroups.map((entry) => ({
			value: entry.representativeDataset,
			label: entry.label,
			icon: kindIcons[entry.kind],
			detail: entry.snapshotCountKnown ? formatSnapshotCount(entry.totalSnapshots) : ''
		}))
	);

	let selectedRestoreTargetDatasetGroup = $derived.by(
		() =>
			restoreTargetDatasetGroups.find((entry) => entry.representativeDataset === dataset) || null
	);

	let selectedRestoreTargetDatasetKind = $derived(
		selectedRestoreTargetDatasetGroup?.kind || 'dataset'
	);

	let restoreTargetSupportsNetworkRestore = $derived(
		selectedRestoreTargetDatasetKind === 'jail' || selectedRestoreTargetDatasetKind === 'vm'
	);
	let guestIdLabel = $derived(selectedRestoreTargetDatasetKind === 'vm' ? 'VM ID' : 'Jail ID');
	let poolOptions = $derived(restorePools.map((pool) => ({ value: pool, label: pool })));

	// A guest always comes back at <pool>/sylve/<kind>/<id>, as a new guest.
	let restoreDestination = $derived.by(() => {
		if (!restoreTargetSupportsNetworkRestore) return destinationDataset.trim();
		const namespace = selectedRestoreTargetDatasetKind === 'vm' ? 'virtual-machines' : 'jails';
		return `${destinationPool}/sylve/${namespace}/${String(destinationGuestId ?? '').trim()}`;
	});

	let jobRunning = $derived.by(() => {
		if (!runningJobStatusAvailable || runningJobIds.size === 0) return false;
		const group = selectedRestoreTargetDatasetGroup;
		if (!group || !group.jobLabel) return false;
		const encodedJobId = jobIdFromLabel(group.jobLabel);
		return encodedJobId > 0 && runningJobIds.has(encodedJobId);
	});

	let runningJobStatusUnavailable = $derived(
		!loadingDatasets && !checkingRunningJobStatus && !!targetId && !runningJobStatusAvailable
	);

	let generationAliasByTag = $derived(buildGenerationAliasMap(snapshots));
	let generationOptions = $derived(buildGenerationOptions(snapshots, generationAliasByTag));
	let visibleSnapshots = $derived(filterSnapshotsByGeneration(snapshots, selectedGeneration));

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

	let selectedSnapshotInfo = $derived(
		snapshots.find((entry) => (entry.name || entry.shortName) === snapshot) || null
	);
	let legacyVMRestoreBlocked = $derived(
		selectedRestoreTargetDatasetKind === 'vm' && !!selectedSnapshotInfo?.legacy
	);
	let restoreDisabledTitle = $derived(
		runningJobStatusUnavailable
			? 'Could not verify active backup operations'
			: jobRunning
				? 'A backup for this lineage is currently in progress'
				: legacyVMRestoreBlocked
					? 'Legacy VM restore points cannot prove that every disk root is complete'
					: ''
	);

	let hasOutOfBandSnapshots = $derived(
		snapshots.some((entry) => !!entry.outOfBand || (entry.lineage || 'active') !== 'active')
	);

	async function loadRestoreClusterDetails(): Promise<ClusterDetails | null> {
		loadingCluster = true;
		try {
			const details = await getDetails();
			if (isAPIResponse(details)) return null;
			clusterDetails = details;
			return details;
		} catch (e: unknown) {
			toast.error((e as { message?: string })?.message || 'Failed to load cluster details', {
				position: 'bottom-center'
			});
			return null;
		} finally {
			loadingCluster = false;
		}
	}

	function resetState(close: boolean = true) {
		targetLoadRevision += 1;
		snapshotLoadRevision += 1;
		loadingDatasets = false;
		loadingSnapshots = false;
		loadingCluster = false;
		restoring = false;
		runningJobIds.clear();
		runningJobStatusAvailable = false;
		checkingRunningJobStatus = false;
		targetId = '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		destinationPool = '';
		destinationGuestId = '';
		poolLoadRevision += 1;
		restorePools = [];
		loadingPools = false;
		restoreNetwork = true;
		encryptionKey = '';
		restoreOperationId = '';
		restoreOperationRequestKey = '';
		datasets = [];
		snapshots = [];
		nextSnapshotCursor = '';
		hasMoreSnapshots = false;
		loadingOlderSnapshots = false;
		olderSnapshotsError = '';
		error = '';
		clusterDetails = null;
		if (close) open = false;
	}

	function guestIdInUse(id: number): ClusterNode | undefined {
		return nodes.find((node) => node.guestIDs?.includes(id));
	}

	// Keeps the chosen pool when the restore node has it, else the backup's own
	// pool, else the node's first pool.
	function pickDestinationPool(preferred: string) {
		if (restorePools.includes(destinationPool)) return;
		destinationPool = restorePools.includes(preferred) ? preferred : restorePools[0] || '';
	}

	// minDelay keeps a node switch from flashing the pool list.
	async function loadRestorePools(minDelay = 0) {
		const revision = ++poolLoadRevision;
		const hostname = nodeNameById[restoreNodeId];
		restorePools = [];
		if (!hostname) return;
		loadingPools = true;
		try {
			const [pools] = await Promise.all([getPools(false, hostname), sleep(minDelay)]);
			if (revision !== poolLoadRevision) return;
			restorePools = pools.map((pool) => pool.name);
			pickDestinationPool(guestSourcePool());
		} catch (e: unknown) {
			if (revision === poolLoadRevision) {
				toast.error((e as { message?: string })?.message || 'Failed to load pools', {
					position: 'bottom-center'
				});
			}
		} finally {
			if (revision === poolLoadRevision) loadingPools = false;
		}
	}

	// A guest's backup path carries the pool it came from: .../<pool>/sylve/jails/<id>/...
	function guestSourcePool(): string {
		return (
			dataset.match(/(?:^|\/)([^/]+)\/sylve\/(?:jails|virtual-machines)\/\d+(?:$|\/)/)?.[1] || ''
		);
	}

	// The backup's own ID while it's free, otherwise the next free one.
	function prefillGuestDestination() {
		const group = selectedRestoreTargetDatasetGroup;
		const sourceId = group?.jailCtId || group?.vmRid || 0;
		destinationGuestId = sourceId > 0 && !guestIdInUse(sourceId) ? sourceId : getNextGuestId(nodes);
		destinationPool = '';
		pickDestinationPool(guestSourcePool());
	}

	async function initializeModal() {
		error = '';
		runningJobIds.clear();
		runningJobStatusAvailable = false;
		checkingRunningJobStatus = true;
		targetId = targetOptions[0]?.value || '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		destinationPool = '';
		destinationGuestId = '';
		restoreNetwork = true;
		encryptionKey = '';
		restoreOperationId = '';
		restoreOperationRequestKey = '';
		datasets = [];
		snapshots = [];
		nextSnapshotCursor = '';
		hasMoreSnapshots = false;
		loadingOlderSnapshots = false;
		olderSnapshotsError = '';
		clusterDetails = null;

		const details = await loadRestoreClusterDetails();
		restoreNodeId = details?.nodeId || nodes[0]?.nodeUUID || '';
		void loadRestorePools();

		if (!targetId) {
			checkingRunningJobStatus = false;
			error = 'No backup targets available';
			return;
		}

		await onTargetChange();
	}

	async function onTargetChange() {
		const loadRevision = ++targetLoadRevision;
		snapshotLoadRevision += 1;
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId) {
			runningJobStatusAvailable = false;
			checkingRunningJobStatus = false;
			return;
		}

		loadingDatasets = true;
		runningJobIds.clear();
		runningJobStatusAvailable = false;
		checkingRunningJobStatus = true;
		error = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		datasets = [];
		snapshots = [];
		nextSnapshotCursor = '';
		hasMoreSnapshots = false;
		loadingOlderSnapshots = false;
		olderSnapshotsError = '';

		try {
			const runningStatus = getTargetRunningJobIds(parsedTargetId).catch(() => null);
			const [targetDatasets, runningIds] = await Promise.all([
				listBackupTargetDatasets(parsedTargetId),
				runningStatus
			]);
			if (loadRevision !== targetLoadRevision) return;

			datasets = targetDatasets;
			if (runningIds !== null) {
				for (const id of runningIds) runningJobIds.add(id);
				runningJobStatusAvailable = true;
			}

			dataset = restoreTargetDatasetGroups[0]?.representativeDataset || '';
			if (dataset) await onDatasetChange();
		} catch (e: unknown) {
			if (loadRevision === targetLoadRevision) {
				error = (e as { message?: string })?.message || 'Failed to load target datasets';
			}
		} finally {
			if (loadRevision === targetLoadRevision) {
				checkingRunningJobStatus = false;
				loadingDatasets = false;
			}
		}
	}

	function prependUniqueSnapshots(older: SnapshotInfo[], current: SnapshotInfo[]): SnapshotInfo[] {
		const seen = new Set(current.map((item) => item.name || item.shortName));
		return [...older.filter((item) => !seen.has(item.name || item.shortName)), ...current];
	}

	async function onDatasetChange() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId || !dataset) return;
		const selectedDataset = dataset;
		const targetRevision = targetLoadRevision;
		const snapshotRevision = ++snapshotLoadRevision;
		const selectedGroup = restoreTargetDatasetGroups.find(
			(entry) => entry.representativeDataset === selectedDataset
		);
		if (!selectedGroup?.encrypted) encryptionKey = '';

		loadingSnapshots = true;
		loadingOlderSnapshots = false;
		olderSnapshotsError = '';
		nextSnapshotCursor = '';
		hasMoreSnapshots = false;
		error = '';
		selectedGeneration = '';
		snapshot = '';
		snapshots = [];

		destinationDataset = '';
		if (selectedGroup?.kind === 'jail' || selectedGroup?.kind === 'vm') prefillGuestDestination();

		try {
			const snapshotPage = await listBackupTargetDatasetSnapshots(parsedTargetId, selectedDataset);
			if (
				targetRevision !== targetLoadRevision ||
				snapshotRevision !== snapshotLoadRevision ||
				selectedDataset !== dataset
			) {
				return;
			}
			const snapshotItems = snapshotPage.items;
			snapshots = snapshotItems;
			nextSnapshotCursor = snapshotPage.nextCursor;
			hasMoreSnapshots = snapshotPage.hasMore && snapshotPage.nextCursor !== '';
			if (snapshotItems.length > 0) {
				const latest = snapshotItems[snapshotItems.length - 1];
				selectedGeneration = snapshotGenerationKey(latest);
				snapshot = latest.name || latest.shortName;
			} else {
				selectedGeneration = '';
				snapshot = '';
			}
		} catch (e: unknown) {
			if (targetRevision === targetLoadRevision && snapshotRevision === snapshotLoadRevision) {
				error = (e as { message?: string })?.message || 'Failed to load dataset details';
			}
		} finally {
			if (targetRevision === targetLoadRevision && snapshotRevision === snapshotLoadRevision) {
				loadingSnapshots = false;
			}
		}
	}

	async function loadOlderSnapshots() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (
			!parsedTargetId ||
			!dataset ||
			!hasMoreSnapshots ||
			!nextSnapshotCursor ||
			loadingOlderSnapshots
		) {
			return;
		}
		const selectedDataset = dataset;
		const cursor = nextSnapshotCursor;
		const targetRevision = targetLoadRevision;
		const snapshotRevision = snapshotLoadRevision;
		loadingOlderSnapshots = true;
		olderSnapshotsError = '';

		try {
			const page = await listBackupTargetDatasetSnapshots(parsedTargetId, selectedDataset, cursor);
			if (
				targetRevision !== targetLoadRevision ||
				snapshotRevision !== snapshotLoadRevision ||
				selectedDataset !== dataset
			) {
				return;
			}
			const hadSnapshots = snapshots.length > 0;
			snapshots = prependUniqueSnapshots(page.items, snapshots);
			if (!hadSnapshots && page.items.length > 0) {
				const latest = page.items[page.items.length - 1];
				selectedGeneration = snapshotGenerationKey(latest);
				snapshot = latest.name || latest.shortName;
			}
			nextSnapshotCursor = page.nextCursor;
			hasMoreSnapshots = page.hasMore && page.nextCursor !== '';
		} catch (e: unknown) {
			if (targetRevision === targetLoadRevision && snapshotRevision === snapshotLoadRevision) {
				olderSnapshotsError =
					(e as { message?: string })?.message || 'Failed to load older snapshots';
			}
		} finally {
			if (targetRevision === targetLoadRevision && snapshotRevision === snapshotLoadRevision) {
				loadingOlderSnapshots = false;
			}
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

	function restoreFailureMessage(response: {
		message?: string;
		error?: string | string[];
	}): string {
		const error = Array.isArray(response.error) ? response.error.join(' ') : response.error || '';
		const text = `${response.message || ''} ${error}`.toLowerCase();

		if (
			text.includes('restore_guest_destination_conflict') ||
			text.includes('guest_id_already_in_use') ||
			text.includes('guest_identity_inventory_conflict') ||
			text.includes('restore_destination_guest_dataset_exists')
		) {
			return 'That RID/CTID or its destination artifacts already exist. Choose an unused ID or clean up the old guest first';
		}
		if (
			text.includes('restore_guest_identity_unavailable') ||
			text.includes('guest_identity_inventory_unavailable')
		) {
			return 'Could not verify the guest ID on every cluster node. Check node health and retry';
		}
		if (
			text.includes('guest_identity_inventory_scan_failed') ||
			text.includes('restore_destination_dataset_check_failed') ||
			text.includes('restore_precheck_failed')
		) {
			return 'Could not safely validate the restore destination. Check server logs and retry';
		}
		if (text.includes('restore_destination_sylve_managed')) {
			return sylveManagedDestinationMessage(restoreDestination);
		}
		if (text.includes('restore_guest_destination_invalid')) {
			return 'The guest ID or destination pool is not valid for this restore';
		}
		if (
			text.includes('backup_job_already_running') ||
			text.includes('restore_destination_already_running') ||
			text.includes('restore_destination_reserved')
		) {
			return 'A restore for this destination is already queued or running';
		}
		if (text.includes('restore_reservation_unavailable')) {
			return 'Could not durably reserve the destination. Check cluster health and retry';
		}
		if (text.includes('restore_vm_legacy_snapshot_unsupported')) {
			return 'Legacy VM restore points cannot prove that every disk root is complete';
		}

		return error || 'Failed to start restore';
	}

	function sylveManagedDestinationMessage(destination: string): string {
		const pool = destination.split('/')[0] || 'pool';
		return `${pool}/sylve is managed by Sylve. Choose a destination outside it`;
	}

	function destinationProblem(): string {
		if (restoreTargetSupportsNetworkRestore) {
			const rawId = String(destinationGuestId ?? '').trim();
			if (!/^\d+$/.test(rawId)) return `${guestIdLabel} must be a number`;
			const id = Number(rawId);
			if (id < 1 || id > 9999) return `${guestIdLabel} must be between 1 and 9999`;
			const owner = guestIdInUse(id);
			if (owner) return `ID ${id} is already used by a guest on ${owner.hostname}`;
			if (!destinationPool) return 'Select a destination pool';
			return '';
		}
		const destination = destinationDataset.trim();
		if (!destination) return 'Destination dataset is required';
		const parts = destination.split('/');
		if (parts.length < 2 || parts.some((part) => part === '')) {
			return 'Destination dataset must be fully qualified (for example: zroot/restored)';
		}
		if (parts[1] === 'sylve') return sylveManagedDestinationMessage(destination);
		return '';
	}

	async function triggerRestoreFromTarget() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId || !dataset || !snapshot) return;
		if (!restoreNodeId.trim()) {
			toast.error('Restore node is required', { position: 'bottom-center' });
			return;
		}
		const problem = destinationProblem();
		if (problem) {
			toast.error(problem, { position: 'bottom-center' });
			return;
		}

		// Re-read durable cluster operation state immediately before submission.
		// A failed status read is unavailable, never evidence that the lineage is idle.
		const statusTarget = targetId;
		const statusDataset = dataset;
		runningJobIds.clear();
		runningJobStatusAvailable = false;
		checkingRunningJobStatus = true;
		let selectedLineageRunning = false;
		try {
			const runningIds = await getTargetRunningJobIds(parsedTargetId);
			if (targetId !== statusTarget || dataset !== statusDataset) {
				toast.error('Restore selection changed while checking cluster status. Retry the restore', {
					position: 'bottom-center'
				});
				return;
			}
			for (const id of runningIds) runningJobIds.add(id);
			const selectedJobId = jobIdFromLabel(selectedRestoreTargetDatasetGroup?.jobLabel || '');
			selectedLineageRunning = selectedJobId > 0 && runningIds.includes(selectedJobId);
			runningJobStatusAvailable = true;
		} catch {
			toast.error('Could not verify active backup operations. Restore remains unavailable', {
				position: 'bottom-center'
			});
			return;
		} finally {
			checkingRunningJobStatus = false;
		}
		if (selectedLineageRunning) {
			toast.error('A backup for this lineage is currently in progress', {
				position: 'bottom-center'
			});
			return;
		}

		const requestKey = JSON.stringify({
			targetId: parsedTargetId,
			remoteDataset: dataset,
			snapshot,
			destinationDataset: restoreDestination,
			restoreNodeId: restoreNodeId.trim(),
			restoreNetwork
		});
		// Keep one idempotency ID across transport retries of the same restore intent.
		if (!restoreOperationId || restoreOperationRequestKey !== requestKey) {
			restoreOperationId = crypto.randomUUID();
			restoreOperationRequestKey = requestKey;
		}

		restoring = true;
		try {
			const response = await restoreBackupFromTarget(parsedTargetId, {
				remoteDataset: dataset,
				snapshot,
				destinationDataset: restoreDestination,
				restoreNodeId: restoreNodeId.trim(),
				restoreNetwork,
				operationId: restoreOperationId,
				encryptionKey,
				encryptionKeyFormat: 'passphrase'
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
			toast.error(restoreFailureMessage(response), { position: 'bottom-center' });
		} catch (e: unknown) {
			toast.error((e as { message?: string })?.message || 'Failed to start restore', {
				position: 'bottom-center'
			});
		} finally {
			restoring = false;
		}
	}

	watch([() => open, () => targetOptions.length], ([isOpen]) => {
		if (!isOpen) {
			targetLoadRevision += 1;
			snapshotLoadRevision += 1;
			return;
		}
		void initializeModal();
	});
</script>

<Dialog.Root
	bind:open
	onOpenChangeComplete={(isOpen) => {
		if (!isOpen) resetState(false);
	}}
>
	<Dialog.Content class="w-full max-w-2xl! overflow-hidden p-6" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--database-sync-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Restore From Target Dataset"
				/>
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
					onChange={() => void loadRestorePools(1000)}
					disabled={loadingCluster || restoreNodeOptions.length === 0}
				/>

				<SimpleSelect
					label="Dataset on Target"
					placeholder={loadingDatasets
						? 'Loading datasets...'
						: restoreTargetDatasetGroups.length === 0
							? 'No restorable datasets found'
							: 'Select dataset'}
					options={restoreTargetDatasetOptions}
					bind:value={dataset}
					onChange={onDatasetChange}
					disabled={loadingDatasets || restoreTargetDatasetGroups.length === 0}
				/>
			</div>

			{#if runningJobStatusUnavailable}
				<div
					class="space-y-2 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-center text-sm text-red-600 dark:text-red-400"
				>
					<p>
						Active backup operations could not be verified. Restore remains unavailable until
						cluster status recovers.
					</p>
					<Button size="sm" variant="outline" onclick={() => void onTargetChange()}>
						Retry Status
					</Button>
				</div>
			{:else if jobRunning}
				<div
					class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-center text-sm text-yellow-700 dark:text-yellow-400"
				>
					A backup for this lineage is currently in progress. Restore is unavailable until it
					completes.
				</div>
			{/if}

			{#if hasOutOfBandSnapshots}
				<div class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-blue-700">
					This backup set includes non-active lineages. Snapshot markers show lineage as
					<code class="rounded bg-background px-1">CURR</code>,
					<code class="rounded bg-background px-1">OOB</code>, and
					<code class="rounded bg-background px-1">INT</code>.
				</div>
			{/if}

			{#if !loadingSnapshots && snapshots.length === 0 && dataset && !hasMoreSnapshots}
				<div
					class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-center text-sm text-blue-700"
				>
					No snapshots found for this dataset. A backup may still be in progress.
				</div>
			{/if}

			{#if loadingSnapshots || snapshots.length > 0 || hasMoreSnapshots || loadingOlderSnapshots || olderSnapshotsError}
				<div class="space-y-1">
					{#if loadingSnapshots || snapshots.length > 0}
						<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
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
						</div>
					{/if}

					{#if hasMoreSnapshots || loadingOlderSnapshots || olderSnapshotsError}
						<div
							class="flex min-h-8 items-center justify-between gap-3 rounded-md bg-muted/40 pl-2 text-xs"
						>
							<div class="min-w-0 flex-1 text-muted-foreground" aria-live="polite">
								{#if olderSnapshotsError}
									<span class="block truncate text-red-500" title={olderSnapshotsError}
										>{olderSnapshotsError}</span
									>
								{:else}
									<span
										>{snapshots.length} {snapshots.length === 1 ? 'backup' : 'backups'} loaded</span
									>
								{/if}
							</div>
							{#if hasMoreSnapshots}
								<Button
									type="button"
									variant="ghost"
									size="sm"
									class="h-8 shrink-0 rounded-none rounded-r-md border-l border-border px-3 text-xs text-foreground"
									onclick={() => void loadOlderSnapshots()}
									disabled={loadingOlderSnapshots}
								>
									{#if loadingOlderSnapshots}
										<span class="icon-[mdi--loading] h-3.5 w-3.5 animate-spin"></span>
										<span>Loading</span>
									{:else if olderSnapshotsError}
										<span class="icon-[mdi--refresh] h-3.5 w-3.5"></span>
										<span>Retry</span>
									{:else}
										<span class="icon-[mdi--chevron-down] h-3.5 w-3.5"></span>
										<span>Load older</span>
									{/if}
								</Button>
							{/if}
						</div>
					{/if}
				</div>
			{/if}

			{#if restoreTargetSupportsNetworkRestore}
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<SimpleSelect
						label="Destination Pool"
						placeholder={loadingPools
							? 'Loading pools...'
							: poolOptions.length === 0
								? 'No pools on this node'
								: 'Select pool'}
						options={poolOptions}
						bind:value={destinationPool}
						onChange={() => {}}
						disabled={loadingPools || poolOptions.length === 0}
						classes={{
							parent: 'flex-1 space-y-1',
							label: 'flex h-7 items-center whitespace-nowrap text-sm',
							trigger:
								'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
						}}
					/>

					<CustomValueInput
						label={guestIdLabel}
						placeholder="100"
						type="number"
						bind:value={destinationGuestId}
						classes="space-y-1"
					/>
				</div>
			{:else}
				<CustomValueInput
					label="Destination Dataset"
					placeholder="pool/path"
					bind:value={destinationDataset}
					classes="space-y-1"
				/>
			{/if}

			{#if selectedSnapshotInfo?.encrypted || selectedRestoreTargetDatasetGroup?.encrypted}
				<div class="space-y-1">
					<CustomValueInput
						label="Encryption Passphrase (Optional)"
						placeholder="Required only when the key is not already registered"
						type="password"
						autocomplete="new-password"
						bind:value={encryptionKey}
						classes="space-y-1"
					/>
					<p class="text-xs text-muted-foreground">
						A supplied passphrase is saved in the cluster key store for automated recovery.
					</p>
				</div>
			{/if}

			{#if restoreTargetSupportsNetworkRestore}
				<CustomCheckbox
					label={selectedRestoreTargetDatasetKind === 'vm'
						? 'Restore VM Network Config'
						: 'Restore Jail Network Config'}
					bind:checked={restoreNetwork}
					classes="flex items-center gap-2"
				/>
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
					{#if selectedRestoreTargetDatasetKind === 'vm' || selectedRestoreTargetDatasetKind === 'jail'}
						<li>
							The backup is restored as a new guest, so its ID must not be in use anywhere in the
							cluster.
						</li>
						{#if selectedRestoreTargetDatasetKind === 'vm'}
							<li>
								The VM's main disk goes to the selected pool. Disks on other pools keep their
								original pool names, which must exist on the restore node.
							</li>
						{/if}
					{:else}
						<li>The destination dataset will be replaced if it already exists.</li>
						<li>
							Datasets under <code class="rounded bg-background px-1">pool/sylve</code> can't be used.
						</li>
					{/if}
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
						{#if selectedSnapshotInfo.legacy}
							<li>
								This restore point predates manifest commits. Legacy VM restore is blocked because
								the complete disk-root set cannot be verified.
							</li>
						{/if}
					{/if}
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button
				onclick={triggerRestoreFromTarget}
				disabled={restoring ||
					loadingDatasets ||
					loadingSnapshots ||
					loadingCluster ||
					!runningJobStatusAvailable ||
					jobRunning ||
					!targetId ||
					!restoreNodeId ||
					!dataset ||
					!snapshot ||
					legacyVMRestoreBlocked}
				title={restoreDisabledTitle}
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
