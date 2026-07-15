<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import {
		getBackupTargetJailMetadata,
		getBackupTargetVMMetadata,
		getTargetRunningJobIds,
		listBackupJobs,
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
		BackupGuestRef,
		BackupJailMetadataInfo,
		BackupJob,
		BackupTarget,
		BackupTargetDatasetInfo,
		BackupVMMetadataInfo,
		RestoreTargetDatasetGroup,
		SnapshotInfo
	} from '$lib/types/cluster/backups';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import {
		buildGenerationAliasMap,
		buildGenerationOptions,
		filterSnapshotsByGeneration,
		formatRestoreSnapshotDate,
		generationLabelFromKey,
		inferJailDestinationDataset,
		inferVMDestinationDataset,
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
		nodes: ClusterNode[];
		reload: boolean;
	}

	let { open = $bindable(), targets, nodes, reload = $bindable() }: Props = $props();

	let loadingDatasets = $state(false);
	let loadingSnapshots = $state(false);
	let loadingCluster = $state(false);
	let restoring = $state(false);
	let runningJobIds = new SvelteSet<number>();
	let allTargetJobs = $state<BackupJob[]>([]);

	let targetId = $state('');
	let restoreNodeId = $state('');
	let dataset = $state('');
	let selectedGeneration = $state('');
	let snapshot = $state('');
	let destinationDataset = $state('');
	let restoreNetwork = $state(true);
	let encryptionKey = $state('');
	let showActiveOnly = $state(true);

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
			if (!Number.isNaN(parsed) && parsed > 0) return { kind: 'jail', id: parsed };
		}
		const vmMatch = datasetPath.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/);
		if (vmMatch) {
			const parsed = Number.parseInt(vmMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) return { kind: 'vm', id: parsed };
		}
		return { kind: 'dataset', id: 0 };
	}

	function formatSnapshotCount(count: number): string {
		return `${count} ${count === 1 ? 'snap' : 'snaps'}`;
	}

	function extractJobLabel(baseSuffix: string): string {
		const match = baseSuffix.match(/(?:^|\/)(job-[0-9]+|j-[0-9a-z]+)(?:\/|$)/i);
		if (!match) return '';
		return match[1];
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
					encrypted: item.encrypted
				});
				continue;
			}
			existing.datasets.push(item);
			existing.totalSnapshots += item.snapshotCount || 0;
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

			const displayBase =
				entry.kind === 'jail' && entry.jailCtId > 0
					? `jails/${entry.jailCtId}`
					: entry.kind === 'vm' && entry.vmRid > 0
						? `virtual-machines/${entry.vmRid}`
						: entry.baseSuffix;

			const totalSnapshots =
				entry.kind === 'vm'
					? Math.max(...entry.datasets.map((d) => d.snapshotCount || 0), 0)
					: entry.totalSnapshots;

			out.push({
				baseSuffix: entry.baseSuffix,
				label: displayBase,
				jobLabel: extractJobLabel(entry.baseSuffix),
				representativeDataset: representative.name,
				kind: entry.kind,
				jailCtId: entry.jailCtId,
				vmRid: entry.vmRid,
				totalSnapshots,
				encrypted: entry.encrypted
			});
		}

		return out.sort((left, right) => left.baseSuffix.localeCompare(right.baseSuffix));
	});

	let filteredDatasetGroups = $derived(
		showActiveOnly
			? restoreTargetDatasetGroups.filter((g) => {
					// Include groups that contain at least one active-lineage dataset.
					const groupDatasets = datasets.filter(
						(d) => (d.baseSuffix || d.suffix || d.name) === g.baseSuffix
					);
					return groupDatasets.some((d) => d.lineage === 'active');
				})
			: restoreTargetDatasetGroups
	);

	let restoreTargetDatasetOptions = $derived(
		filteredDatasetGroups.map((entry) => ({
			value: entry.representativeDataset,
			label: formatRestoreTargetDatasetLabel(entry)
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

	let jobRunning = $derived.by(() => {
		if (runningJobIds.size === 0) return false;
		const group = selectedRestoreTargetDatasetGroup;
		if (!group || !group.jobLabel) return false;
		const matchingJob = allTargetJobs.find(
			(j) => extractJobLabel(j.destSuffix || '') === group.jobLabel
		);
		return matchingJob ? runningJobIds.has(matchingJob.id) : false;
	});

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
		loadingDatasets = false;
		loadingSnapshots = false;
		loadingCluster = false;
		restoring = false;
		runningJobIds.clear();
		allTargetJobs = [];
		targetId = '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		restoreNetwork = true;
		encryptionKey = '';
		showActiveOnly = true;
		datasets = [];
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;
		error = '';
		clusterDetails = null;
		if (close) open = false;
	}

	async function initializeModal() {
		error = '';
		runningJobIds.clear();
		allTargetJobs = [];
		targetId = targetOptions[0]?.value || '';
		restoreNodeId = '';
		dataset = '';
		selectedGeneration = '';
		snapshot = '';
		destinationDataset = '';
		restoreNetwork = true;
		encryptionKey = '';
		showActiveOnly = true;
		datasets = [];
		snapshots = [];
		jailMetadata = null;
		vmMetadata = null;
		clusterDetails = null;

		const details = await loadRestoreClusterDetails();
		restoreNodeId = details?.nodeId || nodes[0]?.nodeUUID || '';

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
		runningJobIds.clear();
		allTargetJobs = [];
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
			const [targetDatasets, targetJobs] = await Promise.all([
				listBackupTargetDatasets(parsedTargetId),
				listBackupJobs(parsedTargetId)
			]);

			allTargetJobs = targetJobs;
			datasets = targetDatasets;

			// Non-fatal: running-job detection is informational only; never block the UI
			try {
				const runningIds = await getTargetRunningJobIds(parsedTargetId);
				for (const id of runningIds) runningJobIds.add(id);
			} catch {
				// ignore — restore remains fully available
			}

			const groupedByBase = new SvelteMap<string, BackupTargetDatasetInfo[]>();
			for (const entry of targetDatasets) {
				const key = entry.baseSuffix || entry.suffix || entry.name;
				if (!groupedByBase.has(key)) groupedByBase.set(key, []);
				groupedByBase.get(key)?.push(entry);
			}

			const representatives: BackupTargetDatasetInfo[] = [];
			for (const group of groupedByBase.values()) {
				const representative = pickRepresentativeDataset(group);
				if (representative) representatives.push(representative);
			}

			representatives.sort((left, right) => {
				const lk = left.baseSuffix || left.suffix || left.name;
				const rk = right.baseSuffix || right.suffix || right.name;
				return lk.localeCompare(rk);
			});

			if (representatives.length > 0) {
				dataset = representatives[0].name;
				await onDatasetChange();
			}
		} catch (e: unknown) {
			error = (e as { message?: string })?.message || 'Failed to load target datasets';
		} finally {
			loadingDatasets = false;
		}
	}

	async function onDatasetChange() {
		const parsedTargetId = Number.parseInt(targetId || '0', 10);
		if (!parsedTargetId || !dataset) return;
		const selectedGroup = restoreTargetDatasetGroups.find(
			(entry) => entry.representativeDataset === dataset
		);
		if (!selectedGroup?.encrypted) encryptionKey = '';

		loadingSnapshots = true;
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
				if (pool) destinationDataset = `${pool}/sylve/virtual-machines/${vmMetadataInfo.rid}`;
			}
		} catch (e: unknown) {
			error = (e as { message?: string })?.message || 'Failed to load dataset details';
		} finally {
			loadingSnapshots = false;
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
		if (
			text.includes('restore_guest_destination_kind_mismatch') ||
			text.includes('restore_guest_destination_must_be_canonical_root') ||
			text.includes('restore_guest_destination_invalid')
		) {
			return 'Guest restores require an exact destination such as pool/sylve/virtual-machines/108 or pool/sylve/jails/108';
		}
		if (text.includes('backup_job_already_running')) {
			return 'A restore for this destination is already running';
		}
		if (text.includes('restore_vm_legacy_snapshot_unsupported')) {
			return 'Legacy VM restore points cannot prove that every disk root is complete';
		}

		return error || 'Failed to start restore';
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
			const response = await restoreBackupFromTarget(parsedTargetId, {
				remoteDataset: dataset,
				snapshot,
				destinationDataset: destinationDataset.trim(),
				restoreNodeId: restoreNodeId.trim(),
				restoreNetwork,
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
			resetState(false);
			return;
		}
		void initializeModal();
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-full max-w-2xl! overflow-hidden p-5" showCloseButton={true}>
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
					onChange={() => {}}
					disabled={loadingCluster || restoreNodeOptions.length === 0}
				/>

				<SimpleSelect
					label="Dataset on Target"
					placeholder={loadingDatasets
						? 'Loading datasets...'
						: filteredDatasetGroups.length === 0
							? 'No restorable datasets found'
							: 'Select dataset'}
					options={restoreTargetDatasetOptions}
					bind:value={dataset}
					onChange={onDatasetChange}
					disabled={loadingDatasets || filteredDatasetGroups.length === 0}
				/>
			</div>

			<CustomCheckbox
				label="Show only active lineage"
				bind:checked={showActiveOnly}
				classes="flex items-center gap-2"
			/>

			{#if jobRunning}
				<div
					class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-center text-sm text-yellow-700 dark:text-yellow-400"
				>
					A backup for this target is currently in progress. Restore is unavailable until it
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

			{#if !loadingSnapshots && snapshots.length === 0 && dataset}
				<div
					class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-center text-sm text-blue-700"
				>
					No snapshots found for this dataset. A backup may still be in progress.
				</div>
			{/if}

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

			{#if selectedSnapshotInfo?.encrypted || selectedRestoreTargetDatasetGroup?.encrypted}
			<div class="space-y-1">
				<CustomValueInput
					label="Encryption Passphrase (Optional)"
					placeholder="Required only when the key is not already registered"
					type="password"
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

			{#if jailMetadata && jailMetadata.ctId > 0}
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

			{#if vmMetadata && vmMetadata.rid > 0}
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
					{#if selectedRestoreTargetDatasetKind === 'vm' || selectedRestoreTargetDatasetKind === 'jail'}
						<li>
							Guest restores are create-only. The RID/CTID and canonical destination dataset
							must not already exist.
						</li>
						{#if selectedRestoreTargetDatasetKind === 'vm'}
							<li>
								The selected VM root uses the destination pool. Any additional VM storage pools
								keep their original pool names and must exist on the restore node.
							</li>
						{/if}
					{:else}
						<li>The destination dataset will be replaced if it already exists.</li>
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
					jobRunning ||
					!targetId ||
					!restoreNodeId ||
					!dataset ||
					!snapshot ||
					!destinationDataset.trim() ||
					legacyVMRestoreBlocked}
				title={legacyVMRestoreBlocked
					? 'Legacy VM restore points cannot prove that every disk root is complete'
					: ''}
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
