<script lang="ts">
	import { createBackupJob, updateBackupJob, type BackupJobInput } from '$lib/api/cluster/backups';
	import { getJails } from '$lib/api/jail/jail';
	import { getVMs } from '$lib/api/vm/vm';
	import { getDatasetsResult } from '$lib/api/zfs/datasets';
	import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		BackupGuestRef,
		BackupGuestScope,
		BackupJob,
		BackupJobMode,
		BackupTarget
	} from '$lib/types/cluster/backups';
	import type { Jail } from '$lib/types/jail/jail';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { vmBaseDataset, vmStoragePools } from '$lib/utils/vm/vm';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { sleep } from '$lib/utils';

	interface Props {
		open: boolean;
		edit: boolean;
		selectedJob: BackupJob | null;
		targets: BackupTarget[];
		nodes: ClusterNode[];
		localNodeId?: string;
		standaloneMode?: boolean;
		guestScope?: BackupGuestScope | null;
		reload: boolean;
	}

	type JobFormState = {
		name: string;
		targetId: string;
		runnerNodeId: string;
		mode: BackupJobMode;
		sourceDataset: string;
		selectedJailId: string;
		selectedVmId: string;
		pruneKeepLast: string;
		pruneTarget: boolean;
		cronExpr: string;
		enabled: boolean;
		stopBeforeBackup: boolean;
		recursive: boolean;
	};

	type SourceEncryptionState = 'idle' | 'checking' | 'encrypted' | 'unencrypted' | 'unavailable';

	let {
		open = $bindable(),
		edit = $bindable(),
		selectedJob,
		targets,
		nodes,
		localNodeId = '',
		standaloneMode = false,
		guestScope = null,
		reload = $bindable()
	}: Props = $props();

	let loading = $state(false);

	let jails = $state<Jail[]>([]);
	let jailsLoading = $state(false);
	let jailsLoadedForNode = $state('');
	let vms = $state<VM[]>([]);
	let vmsLoading = $state(false);
	let vmsLoadedForNode = $state('');
	let lastRunnerNodeId = $state('');
	let srcEncryptionState = $state<SourceEncryptionState>('idle');
	let srcEncryptionGeneration = 0;
	let srcEncryptionController: AbortController | null = null;

	let form = $state<JobFormState>({
		name: '',
		targetId: '',
		runnerNodeId: '',
		mode: 'dataset',
		sourceDataset: '',
		selectedJailId: '',
		selectedVmId: '',
		pruneKeepLast: '0',
		pruneTarget: false,
		cronExpr: '0 * * * *',
		enabled: true,
		stopBeforeBackup: false,
		recursive: false
	});

	let targetOptions = $derived(
		targets.map((target) => ({
			value: String(target.id),
			label: target.name
		}))
	);

	let nodeOptions = $derived.by(() => {
		if (standaloneMode) {
			return nodes.map((node) => ({
				value: node.nodeUUID,
				label: node.hostname
			}));
		}

		return [
			{ value: '', label: 'Select a node' },
			...nodes.map((node) => ({
				value: node.nodeUUID,
				label: node.hostname
			}))
		];
	});

	function normalizedNodeHostname(value: string): string {
		return value.trim().replace(/\s+\(Local\)$/, '');
	}

	let scopedGuest = $derived(guestScope && guestScope.id > 0 ? guestScope : null);
	let scopedGuestNode = $derived.by(() => {
		if (!scopedGuest?.hostname.trim()) return null;
		const hostname = normalizedNodeHostname(scopedGuest.hostname);
		return nodes.find((node) => normalizedNodeHostname(node.hostname) === hostname) || null;
	});
	let scopedGuestLabel = $derived(scopedGuest?.kind === 'vm' ? 'VM' : 'jail');
	let scopedGuestLoading = $derived(
		scopedGuest?.kind === 'vm' ? vmsLoading : scopedGuest?.kind === 'jail' ? jailsLoading : false
	);
	let scopedRebindPending = $derived(
		edit &&
			!!scopedGuest &&
			selectedJob?.mode === scopedGuest.kind &&
			selectedJob?.lastStatus === 'runner_rebind_pending'
	);
	let scopedRunnerMismatch = $derived(
		edit &&
			!!scopedGuest &&
			selectedJob?.mode === scopedGuest.kind &&
			!!scopedGuestNode &&
			form.runnerNodeId.trim() !== scopedGuestNode.nodeUUID.trim()
	);
	let scopedRunnerSelectedForSave = $derived(
		edit &&
			!!scopedGuest &&
			selectedJob?.mode === scopedGuest.kind &&
			!!scopedGuestNode &&
			(selectedJob.runnerNodeId || '').trim() !== scopedGuestNode.nodeUUID.trim() &&
			form.runnerNodeId.trim() === scopedGuestNode.nodeUUID.trim()
	);

	const modeOptions: Array<{ value: BackupJobMode; label: string }> = [
		{ value: 'dataset', label: 'Single Dataset' },
		{ value: 'jail', label: 'Jail' },
		{ value: 'vm', label: 'Virtual Machine' }
	];

	let jailOptions = $derived(
		jails.map((jail) => ({
			value: String(jail.id),
			label: `${jail.name} (CT ${jail.ctId})`
		}))
	);

	let vmOptions = $derived(
		vms.map((vm) => {
			const pools = vmStoragePools(vm);
			const poolLabel = pools.length > 0 ? ` [${pools.join(', ')}]` : '';
			return {
				value: String(vm.id),
				label: `${vm.name} (RID ${vm.rid})${poolLabel}`
			};
		})
	);

	let selectedJail = $derived.by(
		() => jails.find((jail) => jail.id === Number.parseInt(form.selectedJailId || '0', 10)) || null
	);

	let selectedVM = $derived.by(
		() => vms.find((vm) => vm.id === Number.parseInt(form.selectedVmId || '0', 10)) || null
	);

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

	function invalidateSourceEncryptionCheck(state: SourceEncryptionState = 'idle') {
		srcEncryptionGeneration += 1;
		srcEncryptionController?.abort();
		srcEncryptionController = null;
		srcEncryptionState = state;
	}

	async function checkSourceEncryption(dataset: string, hostname: string) {
		const requestedDataset = dataset.trim();
		const requestedHostname = hostname.trim();
		invalidateSourceEncryptionCheck();
		if (!requestedDataset) return;
		if (!requestedHostname) {
			srcEncryptionState = 'unavailable';
			return;
		}

		const generation = srcEncryptionGeneration;
		const controller = new AbortController();
		srcEncryptionController = controller;
		srcEncryptionState = 'checking';

		try {
			await sleep(1000);
			if (controller.signal.aborted || generation !== srcEncryptionGeneration) return;

			const [filesystems, volumes] = await Promise.all([
				getDatasetsResult(
					GZFSDatasetTypeSchema.enum.FILESYSTEM,
					requestedHostname,
					controller.signal
				),
				getDatasetsResult(GZFSDatasetTypeSchema.enum.VOLUME, requestedHostname, controller.signal)
			]);
			if (controller.signal.aborted || generation !== srcEncryptionGeneration) return;
			if (isAPIResponse(filesystems) || isAPIResponse(volumes)) {
				srcEncryptionState = 'unavailable';
				return;
			}

			const source = [...filesystems, ...volumes].find((entry) => entry.name === requestedDataset);
			if (!source) {
				srcEncryptionState = 'unavailable';
				return;
			}
			const encryption = (source.properties?.encryption || '').trim().toLowerCase();
			srcEncryptionState =
				encryption !== '' && encryption !== 'off' && encryption !== '-' && encryption !== 'none'
					? 'encrypted'
					: 'unencrypted';
		} catch (error) {
			if (
				!(error instanceof Error && error.name === 'AbortError') &&
				generation === srcEncryptionGeneration
			) {
				srcEncryptionState = 'unavailable';
			}
		} finally {
			if (generation === srcEncryptionGeneration) {
				srcEncryptionController = null;
				if (srcEncryptionState === 'checking') {
					srcEncryptionState = 'unavailable';
				}
			}
		}
	}

	function selectedRunnerHostname(): string {
		const runnerNodeId = form.runnerNodeId.trim();
		if (!runnerNodeId) return '';

		const selectedNode = nodes.find((node) => node.nodeUUID === runnerNodeId);
		if (selectedNode?.hostname) {
			return normalizedNodeHostname(selectedNode.hostname);
		}

		const normalizedRunner = normalizedNodeHostname(runnerNodeId);
		const nodeByHostname = nodes.find(
			(node) => normalizedNodeHostname(node.hostname) === normalizedRunner
		);
		return nodeByHostname ? normalizedNodeHostname(nodeByHostname.hostname) : '';
	}

	async function loadJails(force: boolean = false) {
		const hostname = selectedRunnerHostname();
		if (jailsLoading) return;
		if (!force && jails.length > 0 && jailsLoadedForNode === hostname) return;
		jailsLoading = true;
		try {
			const res = await getJails(hostname || undefined);
			updateCache(hostname ? `jail-list-${hostname}` : 'jail-list', res);
			jails = res;
			jailsLoadedForNode = hostname;
		} finally {
			jailsLoading = false;
		}
	}

	async function loadVMs(force: boolean = false) {
		const hostname = selectedRunnerHostname();
		if (vmsLoading) return;
		if (!force && vms.length > 0 && vmsLoadedForNode === hostname) return;
		vmsLoading = true;
		try {
			const res = await getVMs(hostname || undefined);
			updateCache(hostname ? `vm-list-${hostname}` : 'vm-list', res);
			vms = res;
			vmsLoadedForNode = hostname;
		} finally {
			vmsLoading = false;
		}
	}

	async function applyDefaults() {
		form.name = '';
		form.targetId = targets[0]?.id ? String(targets[0].id) : '';
		form.runnerNodeId =
			scopedGuestNode?.nodeUUID ||
			(standaloneMode ? localNodeId || nodes[0]?.nodeUUID || '' : (nodes[0]?.nodeUUID ?? ''));
		form.mode = scopedGuest?.kind ?? 'dataset';
		form.sourceDataset = '';
		form.selectedJailId = '';
		form.selectedVmId = '';
		form.pruneKeepLast = '0';
		form.pruneTarget = false;
		form.stopBeforeBackup = false;
		form.recursive = scopedGuest?.kind === 'vm';
		form.cronExpr = '0 * * * *';
		form.enabled = true;
		lastRunnerNodeId = form.runnerNodeId;

		if (scopedGuest?.kind === 'vm') {
			await loadVMs(true);
			const scopedVM = vms.find((vm) => vm.rid === scopedGuest.id);
			form.selectedVmId = scopedVM ? String(scopedVM.id) : '';
		} else if (scopedGuest?.kind === 'jail') {
			await loadJails(true);
			const scopedJail = jails.find((jail) => jail.ctId === scopedGuest.id);
			form.selectedJailId = scopedJail ? String(scopedJail.id) : '';
		}
	}

	async function applyFromJob(job: BackupJob) {
		form.name = job.name;
		form.targetId = String(job.targetId);
		form.runnerNodeId =
			job.runnerNodeId || (standaloneMode ? localNodeId : '') || nodes[0]?.nodeUUID || '';
		form.mode = (job.mode as BackupJobMode) || 'dataset';
		form.sourceDataset = job.sourceDataset || '';
		form.selectedJailId = '';
		form.selectedVmId = '';
		form.pruneKeepLast = String(job.pruneKeepLast ?? 0);
		form.pruneTarget = !!job.pruneTarget;
		form.stopBeforeBackup = !!job.stopBeforeBackup;
		form.recursive = !!job.recursive;
		form.cronExpr = job.cronExpr;
		form.enabled = job.enabled;
		lastRunnerNodeId = form.runnerNodeId;

		if (form.mode === 'jail') {
			await loadJails(true);
			const rootDataset = job.jailRootDataset || job.sourceDataset || '';
			const parsedGuest = parseGuestFromDatasetPath(rootDataset);
			const scopedRunnerMatches =
				scopedGuest?.kind === 'jail' &&
				!!scopedGuestNode &&
				form.runnerNodeId.trim() === scopedGuestNode.nodeUUID.trim();
			const expectedCtID =
				parsedGuest.kind === 'jail' && parsedGuest.id > 0
					? parsedGuest.id
					: scopedRunnerMatches
						? scopedGuest.id
						: 0;
			if (expectedCtID > 0) {
				const matchingJail = jails.find((jail) => jail.ctId === expectedCtID);
				form.selectedJailId = matchingJail ? String(matchingJail.id) : '';
			} else {
				const matchingJail = jails.find((jail) => {
					const baseStorage = jail.storages?.find((storage) => storage.isBase);
					if (!baseStorage) return false;
					return `${baseStorage.pool}/sylve/jails/${jail.ctId}` === rootDataset;
				});
				form.selectedJailId = matchingJail ? String(matchingJail.id) : '';
			}
		}

		if (form.mode === 'vm') {
			await loadVMs(true);
			const parsedGuest = parseGuestFromDatasetPath(job.sourceDataset || '');
			const scopedRunnerMatches =
				scopedGuest?.kind === 'vm' &&
				!!scopedGuestNode &&
				form.runnerNodeId.trim() === scopedGuestNode.nodeUUID.trim();
			const expectedRID =
				parsedGuest.kind === 'vm' && parsedGuest.id > 0
					? parsedGuest.id
					: scopedRunnerMatches
						? scopedGuest.id
						: 0;
			if (expectedRID > 0) {
				const matchingVM = vms.find((vm) => vm.rid === expectedRID);
				form.selectedVmId = matchingVM ? String(matchingVM.id) : '';
			}
		}
	}

	async function useCurrentGuestOwner() {
		if (!scopedGuestNode || !scopedGuest || scopedRebindPending) return;

		if (scopedGuest.kind === 'vm') vmsLoading = true;
		else jailsLoading = true;
		try {
			const hostname = normalizedNodeHostname(scopedGuestNode.hostname);
			if (scopedGuest.kind === 'jail') {
				const result = await getJails(hostname || undefined);
				const scopedJail = result.find((jail) => jail.ctId === scopedGuest.id);
				if (!scopedJail) {
					toast.error(`Jail ${scopedGuest.id} was not found on ${hostname || 'the scoped node'}`, {
						position: 'bottom-center'
					});
					return;
				}
				const baseStorage = scopedJail.storages?.find((storage) => storage.isBase);
				if (!baseStorage) {
					toast.error(`Unable to resolve a dataset root for jail ${scopedGuest.id}`, {
						position: 'bottom-center'
					});
					return;
				}

				updateCache(hostname ? `jail-list-${hostname}` : 'jail-list', result);
				jails = result;
				jailsLoadedForNode = hostname;
				form.runnerNodeId = scopedGuestNode.nodeUUID;
				lastRunnerNodeId = form.runnerNodeId;
				form.mode = 'jail';
				form.selectedJailId = String(scopedJail.id);
				form.selectedVmId = '';
				form.sourceDataset = '';
			} else {
				const result = await getVMs(hostname || undefined);
				const scopedVM = result.find((vm) => vm.rid === scopedGuest.id);
				if (!scopedVM) {
					toast.error(`VM ${scopedGuest.id} was not found on ${hostname || 'the scoped node'}`, {
						position: 'bottom-center'
					});
					return;
				}
				const dataset = vmBaseDataset(scopedVM);
				if (!dataset) {
					toast.error(`Unable to resolve a dataset root for VM ${scopedGuest.id}`, {
						position: 'bottom-center'
					});
					return;
				}

				updateCache(hostname ? `vm-list-${hostname}` : 'vm-list', result);
				vms = result;
				vmsLoadedForNode = hostname;
				form.runnerNodeId = scopedGuestNode.nodeUUID;
				lastRunnerNodeId = form.runnerNodeId;
				form.mode = 'vm';
				form.selectedJailId = '';
				form.selectedVmId = String(scopedVM.id);
				form.sourceDataset = dataset;
				form.recursive = true;
			}

			toast.success(
				`Current ${scopedGuestLabel} owner selected. Save the job to apply the repair.`,
				{
					position: 'bottom-center'
				}
			);
		} catch (e: unknown) {
			toast.error(
				(e as { message?: string })?.message ||
					`Failed to load the scoped ${scopedGuestLabel} owner`,
				{ position: 'bottom-center' }
			);
		} finally {
			if (scopedGuest.kind === 'vm') vmsLoading = false;
			else jailsLoading = false;
		}
	}

	function handleClose() {
		invalidateSourceEncryptionCheck();
		open = false;
		edit = false;
		loading = false;
	}

	async function handleReset() {
		if (edit && selectedJob) {
			await applyFromJob(selectedJob);
			return;
		}

		await applyDefaults();
	}

	async function handleModeChange(value: string) {
		form.mode = value as BackupJobMode;
		form.selectedJailId = '';
		form.selectedVmId = '';
		if (form.mode !== 'dataset') {
			form.sourceDataset = '';
		}
		if (form.mode === 'jail') {
			await loadJails();
		}
		if (form.mode === 'vm') {
			await loadVMs();
		}
	}

	async function saveJob() {
		if (!form.name.trim()) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (!form.targetId) {
			toast.error('Target is required', { position: 'bottom-center' });
			return;
		}
		if (form.mode === 'dataset' && !form.sourceDataset.trim()) {
			toast.error('Source dataset is required for dataset mode', { position: 'bottom-center' });
			return;
		}
		if (form.mode === 'jail' && !form.selectedJailId) {
			toast.error('Jail selection is required for jail mode', { position: 'bottom-center' });
			return;
		}
		if (form.mode === 'vm' && !form.selectedVmId) {
			toast.error('VM selection is required for VM mode', { position: 'bottom-center' });
			return;
		}

		const pruneKeepLast = Number.parseInt(form.pruneKeepLast || '0', 10);
		if (Number.isNaN(pruneKeepLast) || pruneKeepLast < 0) {
			toast.error('Prune keep value must be 0 or greater', { position: 'bottom-center' });
			return;
		}

		let jailDataset = '';
		if (form.mode === 'jail') {
			if (!selectedJail) {
				toast.error('Selected jail was not found', { position: 'bottom-center' });
				return;
			}

			const baseStorage = selectedJail.storages?.find((storage) => storage.isBase);
			if (!baseStorage) {
				toast.error('Unable to resolve a jail base dataset for the selected jail', {
					position: 'bottom-center'
				});
				return;
			}

			jailDataset = `${baseStorage.pool}/sylve/jails/${selectedJail.ctId}`;
		}

		let vmDataset = '';
		if (form.mode === 'vm') {
			if (!selectedVM) {
				toast.error('Selected VM was not found', { position: 'bottom-center' });
				return;
			}

			vmDataset = vmBaseDataset(selectedVM);
			if (!vmDataset) {
				toast.error('Unable to resolve a VM dataset root for the selected VM', {
					position: 'bottom-center'
				});
				return;
			}
		}

		const payload: BackupJobInput = {
			name: form.name,
			targetId: Number.parseInt(form.targetId, 10),
			runnerNodeId:
				standaloneMode && (localNodeId || '').trim() !== ''
					? form.runnerNodeId.trim() || localNodeId.trim()
					: form.runnerNodeId,
			mode: form.mode,
			sourceDataset:
				form.mode === 'dataset' ? form.sourceDataset : form.mode === 'vm' ? vmDataset : '',
			jailRootDataset: form.mode === 'jail' ? jailDataset : '',
			pruneKeepLast,
			pruneTarget: form.pruneTarget,
			stopBeforeBackup: form.stopBeforeBackup,
			recursive: form.recursive,
			cronExpr: form.cronExpr,
			enabled: form.enabled
		};

		loading = true;
		const response = edit
			? await updateBackupJob(selectedJob?.id || 0, payload)
			: await createBackupJob(payload);
		loading = false;

		if (response.status === 'success') {
			toast.success(edit ? 'Backup job updated' : 'Backup job created', {
				position: 'bottom-center'
			});
			reload = true;
			handleClose();
			return;
		}

		handleAPIError(response);
		toast.error(edit ? 'Failed to update job' : 'Failed to create job', {
			position: 'bottom-center'
		});
	}

	watch([() => open, () => edit, () => selectedJob?.id || 0], ([isOpen, isEdit]) => {
		if (!isOpen) return;
		if (isEdit && selectedJob) {
			void applyFromJob(selectedJob);
			return;
		}
		void applyDefaults();
	});

	watch([() => open, () => form.mode, () => form.selectedVmId], ([isOpen, mode, selectedVmId]) => {
		if (!isOpen || mode !== 'vm' || !selectedVmId) return;
		const vm = vms.find((entry) => entry.id === Number.parseInt(selectedVmId, 10));
		if (!vm) return;
		const dataset = vmBaseDataset(vm);
		if (dataset) {
			form.sourceDataset = dataset;
		}
	});

	watch([() => open, () => form.runnerNodeId, () => form.mode], ([isOpen, runnerNodeId, mode]) => {
		if (!isOpen) return;
		if (runnerNodeId === lastRunnerNodeId) return;

		lastRunnerNodeId = runnerNodeId;
		form.selectedJailId = '';
		form.selectedVmId = '';
		if (mode !== 'dataset') {
			form.sourceDataset = '';
		}

		if (mode === 'jail') {
			void loadJails(true);
		}
		if (mode === 'vm') {
			void loadVMs(true);
		}
	});

	watch(
		[() => open, () => form.mode, () => form.sourceDataset, () => form.runnerNodeId],
		([isOpen, mode, dataset]) => {
			if (!isOpen || mode !== 'dataset') {
				invalidateSourceEncryptionCheck();
				return;
			}
			void checkSourceEncryption(dataset, selectedRunnerHostname());
		}
	);

	let disableStopBeforeBackup = $state(false);
	let disableRecursiveBackup = $state(false);

	watch(
		() => form.mode,
		(mode) => {
			if (mode === 'dataset') {
				form.stopBeforeBackup = false;
				disableStopBeforeBackup = true;
			} else {
				disableStopBeforeBackup = false;
			}
			if (mode === 'vm') {
				form.recursive = true;
				disableRecursiveBackup = true;
			} else {
				disableRecursiveBackup = false;
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] w-[90%] max-w-xl! overflow-y-auto p-6"
		showCloseButton={true}
		showResetButton={edit}
		onReset={handleReset}
		onClose={handleClose}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon={edit
						? 'icon-[ic--outline-edit-calendar]'
						: 'icon-[material-symbols--calendar-add-on-outline-rounded]'}
					size="h-5 w-5"
					gap="gap-2"
					title={edit ? 'Edit Backup Job' : 'New Backup Job'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			{#if edit && scopedGuest && selectedJob?.mode === scopedGuest.kind}
				{#if scopedRebindPending}
					<div class="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
						<p class="font-medium text-amber-700 dark:text-amber-300">
							Runner rebind is still in progress
						</p>
						<p class="mt-1 text-muted-foreground">
							Automatic reconciliation owns this job. Wait for it to finish before editing.
						</p>
					</div>
				{:else if !scopedGuestNode}
					<div class="rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm">
						<p class="font-medium text-destructive">
							Scoped {scopedGuestLabel} node is unavailable
						</p>
						<p class="mt-1 text-muted-foreground">
							The route node could not be resolved, so runner repair is disabled.
						</p>
					</div>
				{:else if scopedRunnerMismatch}
					<div class="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
						<p class="font-medium text-amber-700 dark:text-amber-300">
							This job is assigned to a different runner
						</p>
						<p class="mt-1 text-muted-foreground">
							The scoped {scopedGuestLabel} is on {scopedGuestNode.hostname}. Select its current
							owner, then save to repair the assignment.
						</p>
						<Button
							type="button"
							variant="outline"
							size="sm"
							class="mt-3"
							onclick={useCurrentGuestOwner}
							disabled={scopedGuestLoading}
						>
							{scopedGuestLoading
								? `Loading ${scopedGuestLabel} owner…`
								: `Use current ${scopedGuestLabel} owner`}
						</Button>
					</div>
				{:else if scopedRunnerSelectedForSave}
					<div class="rounded-md border border-blue-500/50 bg-blue-500/10 p-3 text-sm">
						<p class="font-medium text-blue-700 dark:text-blue-300">
							Current {scopedGuestLabel} owner selected
						</p>
						<p class="mt-1 text-muted-foreground">
							The runner will change to {scopedGuestNode.hostname} only after this job is saved and the
							backend verifies live placement.
						</p>
					</div>
				{/if}
			{/if}

			<CustomValueInput
				label="Name"
				placeholder="daily-backup"
				bind:value={form.name}
				classes="space-y-1"
			/>

			<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
				<SimpleSelect
					label="Target"
					placeholder="Select target"
					options={targetOptions}
					bind:value={form.targetId}
					onChange={() => {}}
				/>

				<SimpleSelect
					label="Run On Node"
					placeholder="Select node"
					options={nodeOptions}
					bind:value={form.runnerNodeId}
					onChange={() => {}}
					disabled={Boolean(scopedGuest)}
				/>

				<SimpleSelect
					label="Mode"
					placeholder="Select mode"
					options={modeOptions}
					bind:value={form.mode}
					onChange={handleModeChange}
					disabled={Boolean(scopedGuest)}
				/>
			</div>

			<div>
				{#if form.mode === 'dataset'}
					<CustomValueInput
						label="Source Dataset"
						placeholder="zroot/data"
						bind:value={form.sourceDataset}
						classes="space-y-1"
					/>
				{:else if form.mode === 'jail'}
					<SimpleSelect
						label="Jail"
						placeholder={jailsLoading
							? 'Loading jails...'
							: jails.length === 0
								? 'No jails available'
								: 'Select jail'}
						options={jailOptions}
						bind:value={form.selectedJailId}
						onChange={() => {}}
						disabled={jailsLoading || jails.length === 0 || scopedGuest?.kind === 'jail'}
					/>
				{:else}
					<SimpleSelect
						label="Virtual Machine"
						placeholder={vmsLoading
							? 'Loading VMs...'
							: vms.length === 0
								? 'No VMs available'
								: 'Select VM'}
						options={vmOptions}
						bind:value={form.selectedVmId}
						onChange={() => {}}
						disabled={vmsLoading || vms.length === 0 || scopedGuest?.kind === 'vm'}
					/>
				{/if}
			</div>

			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<CustomValueInput
					label="Schedule (Cron, 5-field)"
					placeholder="0 * * * *"
					bind:value={form.cronExpr}
					classes="space-y-1"
				/>

				<CustomValueInput
					label="Keep Last Snapshots"
					placeholder="20 (0 to disable)"
					type="number"
					bind:value={form.pruneKeepLast}
					classes="space-y-1"
				/>
			</div>

			<div class="flex flex-row gap-4">
				<CustomCheckbox
					label="Enabled"
					bind:checked={form.enabled}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="Recursive backup"
					bind:checked={form.recursive}
					classes="flex items-center gap-2"
					disabled={disableRecursiveBackup}
					title={form.mode === 'vm' ? 'VM backups must include all child disk datasets' : ''}
				/>

				<CustomCheckbox
					label="Prune on target"
					bind:checked={form.pruneTarget}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="Stop before backup"
					bind:checked={form.stopBeforeBackup}
					classes="flex items-center gap-2"
					disabled={disableStopBeforeBackup}
					title={form.mode === 'dataset'
						? 'This option is only applicable for jail and VM backups'
						: ''}
				/>
			</div>

			<div class="rounded-md bg-muted p-3 text-sm">
				<p class="font-medium">Job Summary</p>
				<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
					{#if form.mode === 'jail'}
						<li>
							Jail <code class="rounded bg-background px-1"
								>{selectedJail?.name || '(not selected)'}</code
							>
							will be backed up
						</li>
						<li>
							CT ID: <code class="rounded bg-background px-1"
								>{selectedJail?.ctId || '(unknown)'}</code
							>
						</li>
					{:else if form.mode === 'vm'}
						<li>
							VM <code class="rounded bg-background px-1"
								>{selectedVM?.name || '(not selected)'}</code
							>
							will be backed up
						</li>
						<li>
							RID: <code class="rounded bg-background px-1">{selectedVM?.rid || '(unknown)'}</code>
						</li>
					{:else}
						<li>
							Dataset <code class="rounded bg-background px-1"
								>{form.sourceDataset || '(not set)'}</code
							>
							will be backed up
						</li>
					{/if}
					<li>
						Schedule: <code class="rounded bg-background px-1"
							>{cronToHuman(form.cronExpr) || '(not set)'}</code
						>
					</li>
					<li>
						Pruning:
						<code class="rounded bg-background px-1"
							>{Number.parseInt(form.pruneKeepLast || '0', 10) > 0
								? `Keep last ${Number.parseInt(form.pruneKeepLast || '0', 10)} snapshots`
								: 'Disabled'}</code
						>
					</li>
					<li>
						Target prune:
						<code class="rounded bg-background px-1"
							>{form.pruneTarget ? 'Enabled' : 'Disabled'}</code
						>
					</li>

					{#if !disableStopBeforeBackup}
						<li>
							Stop before backup:
							<code class="rounded bg-background px-1"
								>{form.stopBeforeBackup ? 'Enabled' : 'Disabled'}</code
							>
						</li>
					{/if}
					<li>
						Recursive backup:
						<code class="rounded bg-background px-1">{form.recursive ? 'Enabled' : 'Disabled'}</code
						>
					</li>

					{#if form.mode !== 'jail' && form.mode !== 'vm'}
						{#if srcEncryptionState === 'checking'}
							<li>
								<span
									class="icon-[mdi--loading] h-3.5 w-3.5 animate-spin inline-block align-text-bottom mb-0.5"
								></span>
							</li>
						{:else if form.sourceDataset === ''}
							<li>
								<span class="icon-[mdi--help] h-3.5 w-3.5 inline-block align-text-bottom mb-0.5"
								></span>
								<span>Select a dataset to view encryption information</span>
							</li>
						{:else if srcEncryptionState === 'encrypted'}
							<li>
								<span class="icon-[mdi--lock] h-3.5 w-3.5 inline-block align-text-bottom mb-0.5"
								></span>
								<span>Source is encrypted</span>
							</li>
						{:else if srcEncryptionState === 'unencrypted'}
							<li>
								<span class="icon-[mdi--unlocked] h-3.5 w-3.5 inline-block align-text-bottom mb-0.5"
								></span>
								<span>Source is not encrypted</span>
							</li>
						{:else}
							<li>
								<span class="icon-[mdi--help] h-3.5 w-3.5 inline-block align-text-bottom mb-0.5"
								></span>
								<span>Encryption information is unavailable for the selected runner</span>
							</li>
						{/if}
					{/if}
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button onclick={saveJob} disabled={loading || scopedRebindPending}>
				{#if loading}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						<span>{edit ? 'Updating' : 'Creating'}</span>
					</div>
				{:else}
					{edit ? 'Update' : 'Create'}
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
