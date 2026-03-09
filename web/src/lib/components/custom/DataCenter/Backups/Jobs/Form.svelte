<script lang="ts">
	import { createBackupJob, updateBackupJob, type BackupJobInput } from '$lib/api/cluster/backups';
	import { getJails } from '$lib/api/jail/jail';
	import { getVMs } from '$lib/api/vm/vm';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		BackupGuestRef,
		BackupJob,
		BackupJobMode,
		BackupTarget
	} from '$lib/types/cluster/backups';
	import type { Jail } from '$lib/types/jail/jail';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { vmBaseDataset, vmStoragePools } from '$lib/utils/vm/vm';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		edit: boolean;
		selectedJob: BackupJob | null;
		targets: BackupTarget[];
		nodes: ClusterNode[];
		localNodeId?: string;
		standaloneMode?: boolean;
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
	};

	let {
		open = $bindable(),
		edit = $bindable(),
		selectedJob,
		targets,
		nodes,
		localNodeId = '',
		standaloneMode = false,
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
		stopBeforeBackup: false
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

	function selectedRunnerHostname(): string {
		const runnerNodeId = form.runnerNodeId.trim();
		if (!runnerNodeId) return '';

		const selectedNode = nodes.find((node) => node.nodeUUID === runnerNodeId);
		if (selectedNode?.hostname) {
			return selectedNode.hostname;
		}

		const nodeByHostname = nodes.find((node) => node.hostname === runnerNodeId);
		return nodeByHostname?.hostname || runnerNodeId;
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

	function applyDefaults() {
		form.name = '';
		form.targetId = targets[0]?.id ? String(targets[0].id) : '';
		form.runnerNodeId = standaloneMode
			? (localNodeId || nodes[0]?.nodeUUID || '')
			: (nodes[0]?.nodeUUID ?? '');
		form.mode = 'dataset';
		form.sourceDataset = '';
		form.selectedJailId = '';
		form.selectedVmId = '';
		form.pruneKeepLast = '0';
		form.pruneTarget = false;
		form.stopBeforeBackup = false;
		form.cronExpr = '0 * * * *';
		form.enabled = true;
		lastRunnerNodeId = form.runnerNodeId;
	}

	async function applyFromJob(job: BackupJob) {
		form.name = job.name;
		form.targetId = String(job.targetId);
		form.runnerNodeId =
			job.runnerNodeId ||
			(standaloneMode ? localNodeId : '') ||
			nodes[0]?.nodeUUID ||
			'';
		form.mode = (job.mode as BackupJobMode) || 'dataset';
		form.sourceDataset = job.sourceDataset || '';
		form.selectedJailId = '';
		form.selectedVmId = '';
		form.pruneKeepLast = String(job.pruneKeepLast ?? 0);
		form.pruneTarget = !!job.pruneTarget;
		form.stopBeforeBackup = !!job.stopBeforeBackup;
		form.cronExpr = job.cronExpr;
		form.enabled = job.enabled;
		lastRunnerNodeId = form.runnerNodeId;

		if (form.mode === 'jail') {
			await loadJails(true);
			const rootDataset = job.jailRootDataset || job.sourceDataset || '';
			const parsedGuest = parseGuestFromDatasetPath(rootDataset);
			if (parsedGuest.kind === 'jail' && parsedGuest.id > 0) {
				const matchingJail = jails.find((jail) => jail.ctId === parsedGuest.id);
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
			if (parsedGuest.kind === 'vm' && parsedGuest.id > 0) {
				const matchingVM = vms.find((vm) => vm.rid === parsedGuest.id);
				form.selectedVmId = matchingVM ? String(matchingVM.id) : '';
			}
		}
	}

	function handleClose() {
		open = false;
		edit = false;
		loading = false;
	}

	async function handleReset() {
		if (edit && selectedJob) {
			await applyFromJob(selectedJob);
			return;
		}

		applyDefaults();
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
			cronExpr: form.cronExpr,
			enabled: form.enabled,
			stopBeforeBackup: form.stopBeforeBackup
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
		applyDefaults();
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
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-h-[90vh] min-w-1/2 overflow-y-auto p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span
						class="{edit
							? 'icon-[ic--outline-edit-calendar]'
							: 'icon-[material-symbols--calendar-add-on-outline-rounded]'} h-5 w-5"
					></span>
					<span>{edit ? 'Edit Backup Job' : 'New Backup Job'}</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 {edit ? '' : 'hidden'}"
						onclick={handleReset}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>

					<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={handleClose}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
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
				/>

				<SimpleSelect
					label="Mode"
					placeholder="Select mode"
					options={modeOptions}
					bind:value={form.mode}
					onChange={handleModeChange}
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
						disabled={jailsLoading || jails.length === 0}
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
						disabled={vmsLoading || vms.length === 0}
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
					label="Prune on target"
					bind:checked={form.pruneTarget}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="Stop before backup"
					bind:checked={form.stopBeforeBackup}
					classes="flex items-center gap-2"
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
					<li>
						Stop before backup:
						<code class="rounded bg-background px-1"
							>{form.stopBeforeBackup ? 'Enabled' : 'Disabled'}</code
						>
					</li>
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={handleClose}>Cancel</Button>
			<Button onclick={saveJob} disabled={loading}>
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
