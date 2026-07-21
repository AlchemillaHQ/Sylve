<script lang="ts">
	import { createSambaShare, updateSambaShare } from '$lib/api/samba/share';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as Accordion from '$lib/components/ui/accordion/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { Group, User } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { SambaShare } from '$lib/types/samba/shares';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
		users: User[];
		share?: SambaShare | null;
		edit?: boolean;
		reload?: boolean;
		appleExtensions?: boolean;
	}

	type ShareTab = 'details' | 'access' | 'options';
	type AccessMode = 'authenticated' | 'guest-read' | 'guest-write';
	type FormErrorField =
		| 'name'
		| 'dataset'
		| 'access'
		| 'createMask'
		| 'directoryMask'
		| 'timeMachineMaxSize'
		| 'audit';
	type FormErrors = Partial<Record<FormErrorField, string>>;

	interface FormState {
		name: string;
		dataset: { open: boolean; value: string };
		accessMode: AccessMode;
		readUsers: { open: boolean; value: string[] };
		writeUsers: { open: boolean; value: string[] };
		readGroups: { open: boolean; value: string[] };
		writeGroups: { open: boolean; value: string[] };
		createMask: string;
		directoryMask: string;
		timeMachine: boolean;
		timeMachineMaxSize: number;
		auditEnabled: boolean;
		auditedOperations: { open: boolean; value: string[] };
	}

	let {
		open = $bindable(),
		shares,
		datasets,
		groups,
		users,
		share,
		edit = false,
		reload = $bindable(),
		appleExtensions = false
	}: Props = $props();

	const AUDIT_OPERATIONS = [
		{ label: 'connect', value: 'connect' },
		{ label: 'disconnect', value: 'disconnect' },
		{ label: 'create_file', value: 'create_file' },
		{ label: 'mkdirat', value: 'mkdirat' },
		{ label: 'unlinkat', value: 'unlinkat' },
		{ label: 'renameat', value: 'renameat' },
		{ label: 'openat', value: 'openat' },
		{ label: 'close', value: 'close' },
		{ label: 'read', value: 'read' },
		{ label: 'write', value: 'write' }
	];

	const TAB_FIELDS: Record<ShareTab, FormErrorField[]> = {
		details: ['name', 'dataset'],
		access: ['access'],
		options: ['createMask', 'directoryMask', 'timeMachineMaxSize', 'audit']
	};

	const FIELD_TABS: Record<FormErrorField, ShareTab> = {
		name: 'details',
		dataset: 'details',
		access: 'access',
		createMask: 'options',
		directoryMask: 'options',
		timeMachineMaxSize: 'options',
		audit: 'options'
	};

	let userOptions = $derived.by(() => {
		return users.map((user) => ({
			label: user.username,
			value: String(user.id)
		}));
	});

	let groupOptions = $derived.by(() => {
		return groups.map((group) => ({
			label: group.name,
			value: String(group.id)
		}));
	});

	function selectedAccessMode(): AccessMode {
		if (!share?.guest.enabled) return 'authenticated';
		return share.guest.writeable ? 'guest-write' : 'guest-read';
	}

	function createFormState(): FormState {
		return {
			name: share?.name ?? '',
			dataset: {
				open: false,
				value: share?.dataset ?? ''
			},
			accessMode: selectedAccessMode(),
			readUsers: {
				open: false,
				value: share?.permissions.read.users.map((user) => String(user.id)) ?? []
			},
			writeUsers: {
				open: false,
				value: share?.permissions.write.users.map((user) => String(user.id)) ?? []
			},
			readGroups: {
				open: false,
				value: share?.permissions.read.groups.map((group) => String(group.id)) ?? []
			},
			writeGroups: {
				open: false,
				value: share?.permissions.write.groups.map((group) => String(group.id)) ?? []
			},
			createMask: share?.createMask ?? '0664',
			directoryMask: share?.directoryMask ?? '2775',
			timeMachine: share?.timeMachine ?? false,
			timeMachineMaxSize: share?.timeMachineMaxSize ?? 0,
			auditEnabled: share?.auditEnabled ?? false,
			auditedOperations: {
				open: false,
				value: share?.auditedOperations ?? []
			}
		};
	}

	function hasCustomMasks(): boolean {
		return (share?.createMask ?? '0664') !== '0664' || (share?.directoryMask ?? '2775') !== '2775';
	}

	function isSystemDataset(dataset: Dataset): boolean {
		const bootEnvironmentRoot = `${dataset.pool}/ROOT`;
		const sylveRoot = `${dataset.pool}/sylve`;

		return (
			dataset.name === bootEnvironmentRoot ||
			dataset.name.startsWith(`${bootEnvironmentRoot}/`) ||
			dataset.name === sylveRoot ||
			dataset.name.startsWith(`${sylveRoot}/`)
		);
	}

	let form = $state(createFormState());
	let saving = $state(false);
	let activeTab = $state<ShareTab>('details');
	let errors = $state<FormErrors>({});
	let advancedPermissions = $state<string | undefined>(
		hasCustomMasks() ? 'permissions' : undefined
	);

	let datasetOptions = $derived.by(() => {
		const datasetsInUse = new Set(
			shares.filter((existing) => existing.id !== share?.id).map((existing) => existing.dataset)
		);

		return datasets
			.filter(
				(dataset) =>
					dataset.mountpoint !== '-' &&
					dataset.mountpoint !== null &&
					dataset.mountpoint !== '' &&
					dataset.mountpoint !== '/'
			)
			.filter((dataset) => !isSystemDataset(dataset))
			.filter((dataset) => !datasetsInUse.has(dataset.guid ? dataset.guid : dataset.name))
			.map((dataset) => ({
				label: dataset.name,
				value: dataset.guid ? dataset.guid : dataset.name
			}));
	});

	function normalizeWriteWins() {
		const writeUsers = new Set(form.writeUsers.value);
		const readUsers = form.readUsers.value.filter((id) => !writeUsers.has(id));
		if (readUsers.length !== form.readUsers.value.length) {
			form.readUsers.value = readUsers;
		}

		const writeGroups = new Set(form.writeGroups.value);
		const readGroups = form.readGroups.value.filter((id) => !writeGroups.has(id));
		if (readGroups.length !== form.readGroups.value.length) {
			form.readGroups.value = readGroups;
		}
	}

	function toIDList(values: string[]): number[] {
		return values
			.map((value) => Number(value))
			.filter((value) => Number.isFinite(value) && value > 0);
	}

	function accessCardClass(mode: AccessMode): string {
		return form.accessMode === mode
			? 'border-primary bg-primary/5 ring-primary/20 ring-1'
			: 'border-border bg-background hover:bg-muted/50';
	}

	function accessSummary(): string {
		if (form.accessMode === 'guest-read') return 'Guest read-only access';
		if (form.accessMode === 'guest-write') return 'Guest read and write access';

		const principalCount =
			form.readUsers.value.length +
			form.writeUsers.value.length +
			form.readGroups.value.length +
			form.writeGroups.value.length;

		return principalCount > 0
			? `Authenticated access for ${principalCount} selection${principalCount === 1 ? '' : 's'}`
			: 'Authenticated access not configured';
	}

	function selectedDatasetLabel(): string {
		if (!form.dataset.value) return 'Not set';
		return datasetOptions.find((dataset) => dataset.value === form.dataset.value)?.label ?? form.dataset.value;
	}

	function tabError(tab: ShareTab): string {
		for (const field of TAB_FIELDS[tab]) {
			if (errors[field]) return errors[field] ?? '';
		}
		return '';
	}

	function clearError(field: FormErrorField) {
		if (errors[field]) errors[field] = undefined;
	}

	function showValidationError(field: FormErrorField, message: string): false {
		errors[field] = message;
		activeTab = FIELD_TABS[field];
		if (field === 'createMask' || field === 'directoryMask') advancedPermissions = 'permissions';
		toast.error(message, { position: 'bottom-center' });
		return false;
	}

	function validateForm(): boolean {
		errors = {};

		const name = form.name.trim();
		if (!name) return showValidationError('name', 'Name is required');
		if (/[\r\n\[\]]/.test(name)) {
			return showValidationError('name', 'Name cannot contain brackets or line breaks');
		}
		if (shares.some((existing) => existing.name === name && existing.id !== share?.id)) {
			return showValidationError('name', 'Share name already exists');
		}

		if (!form.dataset.value) return showValidationError('dataset', 'Dataset is required');
		if (
			shares.some(
				(existing) => existing.dataset === form.dataset.value && existing.id !== share?.id
			)
		) {
			return showValidationError('dataset', 'This dataset is already used by another Samba share');
		}

		const totalPrincipals =
			form.readUsers.value.length +
			form.writeUsers.value.length +
			form.readGroups.value.length +
			form.writeGroups.value.length;
		if (form.accessMode === 'authenticated' && totalPrincipals === 0) {
			return showValidationError('access', 'Select at least one user or group for authenticated access');
		}

		if (!/^[0-7]{4}$/.test(form.createMask.trim())) {
			return showValidationError('createMask', 'Create mask must contain exactly four octal digits');
		}
		if (!/^[0-7]{4}$/.test(form.directoryMask.trim())) {
			return showValidationError('directoryMask', 'Directory mask must contain exactly four octal digits');
		}

		if (
			form.timeMachine &&
			(!Number.isInteger(form.timeMachineMaxSize) || form.timeMachineMaxSize < 0)
		) {
			return showValidationError('timeMachineMaxSize', 'Time Machine size must be a whole number of 0 or more');
		}
		if (form.auditEnabled && form.auditedOperations.value.length === 0) {
			return showValidationError('audit', 'Select at least one operation to audit');
		}

		return true;
	}

	function resetForm() {
		form = createFormState();
		errors = {};
		activeTab = 'details';
		advancedPermissions = hasCustomMasks() ? 'permissions' : undefined;
	}

	function closeDialog() {
		resetForm();
		open = false;
	}

	async function createOrEdit() {
		if (!validateForm()) return;
		if (edit && !share) {
			toast.error('Unable to find the Samba share to update', { position: 'bottom-center' });
			return;
		}

		const authenticatedAccess = form.accessMode === 'authenticated';
		const permissions = {
			read: {
				userIds: authenticatedAccess ? toIDList(form.readUsers.value) : [],
				groupIds: authenticatedAccess ? toIDList(form.readGroups.value) : []
			},
			write: {
				userIds: authenticatedAccess ? toIDList(form.writeUsers.value) : [],
				groupIds: authenticatedAccess ? toIDList(form.writeGroups.value) : []
			}
		};
		const guest = {
			enabled: !authenticatedAccess,
			writeable: form.accessMode === 'guest-write'
		};

		let response: APIResponse;
		saving = true;

		if (edit && share) {
			response = await updateSambaShare(
				share.id,
				form.name.trim(),
				form.dataset.value,
				permissions,
				guest,
				form.createMask.trim(),
				form.directoryMask.trim(),
				form.timeMachine,
				form.timeMachine ? form.timeMachineMaxSize : 0,
				form.auditEnabled,
				form.auditEnabled ? form.auditedOperations.value : []
			);
		} else {
			response = await createSambaShare(
				form.name.trim(),
				form.dataset.value,
				permissions,
				guest,
				form.createMask.trim(),
				form.directoryMask.trim(),
				form.timeMachine,
				form.timeMachine ? form.timeMachineMaxSize : 0,
				form.auditEnabled,
				form.auditEnabled ? form.auditedOperations.value : []
			);
		}

		saving = false;

		if (response.status === 'error') {
			handleAPIError(response);
			toast.error(`Failed to ${edit ? 'edit' : 'create'} Samba share`, {
				position: 'bottom-center'
			});
			return;
		}

		toast.success(`Samba share ${edit ? 'edited' : 'created'}`, {
			position: 'bottom-center'
		});
		reload = true;
		closeDialog();
	}

	watch(
		[
			() => form.readUsers.value,
			() => form.writeUsers.value,
			() => form.readGroups.value,
			() => form.writeGroups.value
		],
		() => normalizeWriteWins()
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden p-5 sm:h-[82dvh] sm:max-h-[42rem] sm:w-[calc(100vw-4rem)] sm:max-w-4xl! sm:p-6"
		showCloseButton={true}
		showResetButton={edit}
		onReset={resetForm}
		onClose={closeDialog}
	>
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title>
				<SpanWithIcon
					icon={edit ? 'icon-[mdi--folder-edit-outline]' : 'icon-[mdi--folder-network]'}
					size="h-5 w-5"
					gap="gap-2"
					title={edit ? `Edit Samba Share - ${share?.name ?? ''}` : 'Create Samba Share'}
				/>
			</Dialog.Title>
			<Dialog.Description class="mt-1 text-xs">
				Choose the share location, define access, then enable optional Samba features.
			</Dialog.Description>
		</Dialog.Header>

		<Tabs.Root bind:value={activeTab} class="mt-4 min-h-0 flex-1 gap-0">
			<Tabs.List class="grid w-full shrink-0 grid-cols-3 p-0">
				<Tabs.Trigger
					value="details"
					class="border-b px-1 text-xs sm:text-sm"
					title={tabError('details') || undefined}
				>
					<span class="icon-[mdi--folder-outline] h-4 w-4"></span>
					Details
					{#if tabError('details')}
						<span class="icon-[mdi--alert-circle-outline] text-destructive h-3.5 w-3.5"></span>
						<span class="sr-only">Needs attention</span>
					{/if}
				</Tabs.Trigger>
				<Tabs.Trigger
					value="access"
					class="border-b px-1 text-xs sm:text-sm"
					title={tabError('access') || undefined}
				>
					<span class="icon-[mdi--account-key-outline] h-4 w-4"></span>
					Access
					{#if tabError('access')}
						<span class="icon-[mdi--alert-circle-outline] text-destructive h-3.5 w-3.5"></span>
						<span class="sr-only">Needs attention</span>
					{/if}
				</Tabs.Trigger>
				<Tabs.Trigger
					value="options"
					class="border-b px-1 text-xs sm:text-sm"
					title={tabError('options') || undefined}
				>
					<span class="icon-[mdi--tune-variant] h-4 w-4"></span>
					Options
					{#if tabError('options')}
						<span class="icon-[mdi--alert-circle-outline] text-destructive h-3.5 w-3.5"></span>
						<span class="sr-only">Needs attention</span>
					{/if}
				</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="details" class="m-0 min-h-0 flex-1 overflow-y-auto py-4 pr-3">
				<div class="space-y-5 pb-1">
					{#if tabError('details')}
						<div
							class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
							role="alert"
						>
							{tabError('details')}
						</div>
					{/if}

					<section class="space-y-3" aria-labelledby="share-details-heading">
						<div>
							<p id="share-details-heading" class="text-sm font-semibold">Share details</p>
							<p class="text-muted-foreground mt-1 text-xs">
								The selected dataset is the directory exposed by this share.
							</p>
						</div>

						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<CustomValueInput
								label="Name"
								placeholder="share"
								bind:value={form.name}
								onChange={() => clearError('name')}
								classes="space-y-1.5"
								inputClasses={errors.name ? 'border-destructive' : ''}
							/>

							<CustomComboBox
								label="Dataset"
								placeholder={datasetOptions.length ? 'Select dataset' : 'No datasets available'}
								bind:open={form.dataset.open}
								bind:value={form.dataset.value}
								data={datasetOptions}
								onValueChange={() => clearError('dataset')}
								classes="space-y-1.5"
								buttonClass={errors.dataset ? 'h-9 border-destructive' : 'h-9'}
								width="w-full"
							/>
						</div>

						<p class="text-muted-foreground text-xs">
							Only mounted, non-system datasets that are not already used by another Samba share are shown.
						</p>
					</section>
				</div>
			</Tabs.Content>

			<Tabs.Content value="access" class="m-0 min-h-0 flex-1 overflow-y-auto py-4 pr-3">
				<div class="space-y-5 pb-1">
					{#if tabError('access')}
						<div
							class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
							role="alert"
						>
							{tabError('access')}
						</div>
					{/if}

					<section class="space-y-3" aria-labelledby="access-mode-heading">
						<div>
							<p id="access-mode-heading" class="text-sm font-semibold">Access mode</p>
							<p class="text-muted-foreground mt-1 text-xs">
								Choose who can connect to this share. Guest access does not include your named permissions.
							</p>
						</div>

						<div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
							<label class={`focus-within:ring-ring/50 focus-within:border-ring cursor-pointer rounded-md border p-3 transition-colors focus-within:ring-[3px] ${accessCardClass('authenticated')}`}>
								<input
									class="sr-only"
									type="radio"
									name="access-mode"
									value="authenticated"
									bind:group={form.accessMode}
									onchange={() => clearError('access')}
								/>
								<span class="flex items-center gap-2 text-sm font-medium">
									<span class="icon-[mdi--account-group-outline] h-4 w-4"></span>
									Authenticated
								</span>
								<span class="text-muted-foreground mt-1 block text-xs">Specific users and groups</span>
							</label>

							<label class={`focus-within:ring-ring/50 focus-within:border-ring cursor-pointer rounded-md border p-3 transition-colors focus-within:ring-[3px] ${accessCardClass('guest-read')}`}>
								<input
									class="sr-only"
									type="radio"
									name="access-mode"
									value="guest-read"
									bind:group={form.accessMode}
									onchange={() => clearError('access')}
								/>
								<span class="flex items-center gap-2 text-sm font-medium">
									<span class="icon-[mdi--account-eye-outline] h-4 w-4"></span>
									Guest read-only
								</span>
								<span class="text-muted-foreground mt-1 block text-xs">Anyone can browse and read files</span>
							</label>

							<label class={`focus-within:ring-ring/50 focus-within:border-ring cursor-pointer rounded-md border p-3 transition-colors focus-within:ring-[3px] ${accessCardClass('guest-write')}`}>
								<input
									class="sr-only"
									type="radio"
									name="access-mode"
									value="guest-write"
									bind:group={form.accessMode}
									onchange={() => clearError('access')}
								/>
								<span class="flex items-center gap-2 text-sm font-medium">
									<span class="icon-[mdi--account-edit-outline] h-4 w-4"></span>
									Guest read/write
								</span>
								<span class="text-muted-foreground mt-1 block text-xs">Anyone can read and modify files</span>
							</label>
						</div>
					</section>

					{#if form.accessMode === 'authenticated'}
						<section class="space-y-3" aria-labelledby="permission-heading">
							<div>
								<p id="permission-heading" class="text-sm font-semibold">Named permissions</p>
								<p class="text-muted-foreground mt-1 text-xs">
									Write access takes precedence over read access for the same user or group.
								</p>
							</div>

							<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
								<CustomComboBox
									label="Read Users"
									placeholder="Select users"
									bind:open={form.readUsers.open}
									bind:value={form.readUsers.value}
									data={userOptions}
									onValueChange={() => clearError('access')}
									multiple={true}
									showCount={true}
									showCountLabel=" users"
									classes="space-y-1.5"
									buttonClass={errors.access ? 'h-9 border-destructive' : 'h-9'}
									width="w-full"
								/>

								<CustomComboBox
									label="Write Users"
									placeholder="Select users"
									bind:open={form.writeUsers.open}
									bind:value={form.writeUsers.value}
									data={userOptions}
									onValueChange={() => clearError('access')}
									multiple={true}
									showCount={true}
									showCountLabel=" users"
									classes="space-y-1.5"
									buttonClass={errors.access ? 'h-9 border-destructive' : 'h-9'}
									width="w-full"
								/>

								<CustomComboBox
									label="Read Groups"
									placeholder="Select groups"
									bind:open={form.readGroups.open}
									bind:value={form.readGroups.value}
									data={groupOptions}
									onValueChange={() => clearError('access')}
									multiple={true}
									showCount={true}
									showCountLabel=" groups"
									classes="space-y-1.5"
									buttonClass={errors.access ? 'h-9 border-destructive' : 'h-9'}
									width="w-full"
								/>

								<CustomComboBox
									label="Write Groups"
									placeholder="Select groups"
									bind:open={form.writeGroups.open}
									bind:value={form.writeGroups.value}
									data={groupOptions}
									onValueChange={() => clearError('access')}
									multiple={true}
									showCount={true}
									showCountLabel=" groups"
									classes="space-y-1.5"
									buttonClass={errors.access ? 'h-9 border-destructive' : 'h-9'}
									width="w-full"
								/>
							</div>
						</section>
					{:else}
						<div class="rounded-md border border-primary/25 bg-primary/5 p-4 text-sm">
							<p class="font-medium">
								{form.accessMode === 'guest-read'
									? 'Guests can read this share without credentials.'
									: 'Guests can read and write this share without credentials.'}
							</p>
							<p class="text-muted-foreground mt-1 text-xs">
								Your named user and group selections are preserved if you switch back to authenticated access.
							</p>
						</div>
					{/if}
				</div>
			</Tabs.Content>

			<Tabs.Content value="options" class="m-0 min-h-0 flex-1 overflow-y-auto py-4 pr-3">
				<div class="space-y-5 pb-1">
					{#if tabError('options')}
						<div
							class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
							role="alert"
						>
							{tabError('options')}
						</div>
					{/if}

					<section class="space-y-3" aria-labelledby="services-heading">
						<div>
							<p id="services-heading" class="text-sm font-semibold">Services</p>
							<p class="text-muted-foreground mt-1 text-xs">
								Enable the Samba features needed by clients of this share.
							</p>
						</div>

						<div class={appleExtensions ? 'grid grid-cols-1 gap-3 sm:grid-cols-2' : ''}>
							{#if appleExtensions}
								<div class="space-y-3">
									<div class="flex items-start justify-between gap-4 rounded-md border p-4">
										<div class="space-y-1">
											<Label for="time-machine" class="text-sm font-medium">Time Machine</Label>
											<p class="text-muted-foreground text-xs">
												Advertise this share as a Time Machine backup destination.
											</p>
										</div>
										<Checkbox
											id="time-machine"
											bind:checked={form.timeMachine}
											onchange={() => clearError('timeMachineMaxSize')}
										/>
									</div>

									{#if form.timeMachine}
										<CustomValueInput
											label="Time Machine Max Size (GB)"
											placeholder="0"
											bind:value={form.timeMachineMaxSize}
											onChange={() => clearError('timeMachineMaxSize')}
											classes="space-y-1.5"
											inputClasses={errors.timeMachineMaxSize ? 'border-destructive' : ''}
											hint="Use 0 for no limit. Whole numbers only."
											type="number"
										/>
									{/if}
								</div>
							{/if}

							<div class="space-y-3">
								<div class="flex items-start justify-between gap-4 rounded-md border p-4">
									<div class="space-y-1">
										<Label for="audit-enabled" class="text-sm font-medium">Audit logging</Label>
										<p class="text-muted-foreground text-xs">
											Record selected file operations for this share.
										</p>
									</div>
									<Checkbox
										id="audit-enabled"
										bind:checked={form.auditEnabled}
										onchange={() => clearError('audit')}
									/>
								</div>

								{#if form.auditEnabled}
									<CustomComboBox
										label="Operations to Audit"
										placeholder="Select operations"
										bind:open={form.auditedOperations.open}
										bind:value={form.auditedOperations.value}
										data={AUDIT_OPERATIONS}
										onValueChange={() => clearError('audit')}
										multiple={true}
										showCount={true}
										showCountLabel=" operations"
										classes="space-y-1.5"
										buttonClass={errors.audit ? 'h-9 border-destructive' : 'h-9'}
										width="w-full"
									/>
								{/if}
							</div>
						</div>
					</section>

					<Accordion.Root
						type="single"
						collapsible
						bind:value={advancedPermissions}
						class="rounded-md border bg-muted/10 px-4"
					>
						<Accordion.Item value="permissions" class="border-b-0">
							<Accordion.Trigger
								class="py-3 text-xs uppercase tracking-widest text-muted-foreground hover:no-underline"
							>
								<span class="flex min-w-0 flex-1 items-center justify-between gap-3">
									<span>Advanced permissions</span>
									<span class="normal-case tracking-normal">Default: 0664 files, 2775 directories</span>
								</span>
							</Accordion.Trigger>
							<Accordion.Content class="pb-4">
								<p class="text-muted-foreground mb-4 text-xs">
									Use four octal digits to control permissions for newly created files and directories.
								</p>
								<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
									<CustomValueInput
										label="Create Mask"
										placeholder="0664"
										bind:value={form.createMask}
										onChange={() => clearError('createMask')}
										classes="space-y-1.5"
										inputClasses={errors.createMask ? 'border-destructive' : ''}
										hint="For example, 0664 gives owner read/write and group read/write."
									/>

									<CustomValueInput
										label="Directory Mask"
										placeholder="2775"
										bind:value={form.directoryMask}
										onChange={() => clearError('directoryMask')}
										classes="space-y-1.5"
										inputClasses={errors.directoryMask ? 'border-destructive' : ''}
										hint="For example, 2775 preserves the shared group on new directories."
									/>
								</div>
							</Accordion.Content>
						</Accordion.Item>
					</Accordion.Root>

					<section class="rounded-md border bg-muted/40 p-4" aria-labelledby="share-summary-heading">
						<p id="share-summary-heading" class="text-sm font-semibold">Share summary</p>
						<dl class="mt-3 grid gap-3 text-sm sm:grid-cols-2">
							<div>
								<dt class="text-muted-foreground text-xs">Name</dt>
								<dd class="mt-1 font-medium">{form.name.trim() || 'Not set'}</dd>
							</div>
							<div>
								<dt class="text-muted-foreground text-xs">Dataset</dt>
								<dd class="mt-1 break-all font-medium">{selectedDatasetLabel()}</dd>
							</div>
							<div>
								<dt class="text-muted-foreground text-xs">Access</dt>
								<dd class="mt-1 font-medium">{accessSummary()}</dd>
							</div>
							<div>
								<dt class="text-muted-foreground text-xs">Services</dt>
								<dd class="mt-1 font-medium">
									{#if (appleExtensions && form.timeMachine) || form.auditEnabled}
										{appleExtensions && form.timeMachine ? 'Time Machine' : ''}{appleExtensions &&
										form.timeMachine &&
										form.auditEnabled
											? ' and '
											: ''}{form.auditEnabled ? 'Audit logging' : ''}
									{:else}
										Standard Samba share
									{/if}
								</dd>
							</div>
						</dl>
					</section>
				</div>
			</Tabs.Content>
		</Tabs.Root>

		<Dialog.Footer class="shrink-0 border-t pt-4">
			<Button onclick={createOrEdit} disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					<span>{edit ? 'Saving...' : 'Creating...'}</span>
				{:else}
					{edit ? 'Save Changes' : 'Create Share'}
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
