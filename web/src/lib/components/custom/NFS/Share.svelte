<script lang="ts">
	import { getNFSDatasets, saveNFSShare } from '$lib/api/nfs';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import {
		NFSMutationSchema,
		NFSDatasetSchema,
		NFSValidationErrorSchema,
		type NFSDataset,
		type NFSShare
	} from '$lib/types/nfs';
	import { cachedFetch, handleAPIError } from '$lib/utils/http';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(),
		node,
		share = null,
		datasets = $bindable(),
		reload = $bindable(false)
	}: {
		open: boolean;
		node: string;
		share?: NFSShare | null;
		datasets: NFSDataset[];
		reload?: boolean;
	} = $props();
	const targetNode = untrack(() => node);
	const initialShare = untrack(() => share);
	let datasetsLoading = $state(true);
	let datasetsError = $state('');
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});
	onMount(() => {
		const controller = new AbortController();
		void cachedFetch(
			'nfs-datasets',
			() => getNFSDatasets({ hostname: targetNode, signal: controller.signal }),
			60_000,
			false,
			targetNode,
			NFSDatasetSchema.array()
		)
			.then((result) => {
				if (mounted) datasets = result;
			})
			.catch(() => {
				if (mounted) datasetsError = 'Unable to load filesystems.';
			})
			.finally(() => {
				if (mounted) datasetsLoading = false;
			});
		return () => controller.abort();
	});

	type ShareTab = 'details' | 'access';
	const FIELD_TABS = {
		dataset: 'details',
		description: 'details',
		clients: 'access',
		security: 'access',
		maprootUser: 'access',
		maprootGroup: 'access',
		mapallUser: 'access',
		mapallGroup: 'access'
	} satisfies Record<string, ShareTab>;
	type ErrorField = keyof typeof FIELD_TABS;
	function initialForm() {
		return {
			description: initialShare?.description ?? '',
			enabled: initialShare?.enabled ?? true,
			readOnly: initialShare?.readOnly ?? true,
			everyone: initialShare?.everyone ?? false,
			clients: {
				open: false,
				value: (initialShare?.clients.slice() ?? []) as string | string[],
				options: (initialShare?.clients ?? []).map((value) => ({ value, label: value }))
			},
			maprootUser: initialShare?.maprootUser ?? '',
			maprootGroup: initialShare?.maprootGroup ?? '',
			mapallUser: initialShare?.mapallUser ?? '',
			mapallGroup: initialShare?.mapallGroup ?? '',
			allDirs: initialShare?.allDirs ?? false
		};
	}
	let form = $state(untrack(initialForm));
	let datasetValue = $state<string | string[]>(untrack(() => initialShare?.dataset ?? ''));
	let datasetOpen = $state(false);
	let securityOpen = $state(false);
	let security = $state<string | string[]>(
		untrack(() => initialShare?.security.slice() ?? ['sys'])
	);
	let activeTab = $state<ShareTab>('details');
	let errors = $state<Partial<Record<ErrorField, string>>>({});
	let saving = $state(false);
	let confirmOpen = $state(false);
	let preservesRoot = $derived(
		[form.maprootUser, form.mapallUser].some((name) => ['root', '0'].includes(name.trim()))
	);
	let datasetGUID = $derived(typeof datasetValue === 'string' ? datasetValue : '');
	let needsDataset = $derived(
		!initialShare || form.enabled || datasetGUID !== initialShare.dataset
	);
	let dataset = $derived(datasets.find((entry) => entry.guid === datasetGUID));
	let clients = $derived(
		Array.isArray(form.clients.value)
			? form.clients.value.map((value) => value.trim()).filter(Boolean)
			: []
	);
	let datasetOptions = $derived.by(() => {
		const options = datasets.map((entry) => ({
			value: entry.guid,
			label: entry.available ? entry.name : `${entry.name} (Unavailable)`,
			disabled: !entry.available,
			description: entry.reason || entry.mountpoint
		}));
		if (initialShare && !options.some((entry) => entry.value === initialShare.dataset)) {
			options.push({
				value: initialShare.dataset,
				label: `${initialShare.dataset} (Unavailable)`,
				disabled: true,
				description: 'The saved filesystem is unavailable.'
			});
		}
		return options;
	});
	function tabError(tab: ShareTab) {
		const field = (Object.keys(FIELD_TABS) as ErrorField[]).find(
			(field) => FIELD_TABS[field] === tab && errors[field]
		);
		return field ? errors[field] : undefined;
	}
	function validate() {
		errors = {};
		if (!Array.isArray(security) || security.length === 0)
			errors.security = 'Select a security flavor.';
		if (datasetsLoading && needsDataset) errors.dataset = 'Loading filesystems...';
		else if (!datasetGUID) errors.dataset = 'Select a filesystem.';
		else if ((!dataset?.available || datasetsError) && needsDataset) {
			errors.dataset = 'Select an available filesystem, or disable this saved share.';
		}
		if (new TextEncoder().encode(form.description.trim()).length > 256) {
			errors.description = 'Description must be at most 256 bytes.';
		}
		if (!form.everyone && clients.length === 0) {
			errors.clients = 'Specify at least one client, or explicitly select Everyone.';
		} else if (!form.everyone && clients.length > 64) {
			errors.clients = 'A share supports at most 64 client addresses or networks.';
		}
		if (form.maprootGroup.trim() && !form.maprootUser.trim())
			errors.maprootGroup = 'Select a Maproot user.';
		if (form.mapallGroup.trim() && !form.mapallUser.trim())
			errors.mapallGroup = 'Select a Mapall user.';
		if (form.maprootUser.trim() && form.mapallUser.trim())
			errors.mapallUser = 'Maproot and Mapall cannot be combined.';
		const field = (Object.keys(errors) as ErrorField[])[0];
		if (field) activeTab = FIELD_TABS[field];
		return !field;
	}
	function reset() {
		form = initialForm();
		security = initialShare?.security.slice() ?? ['sys'];
		datasetValue = initialShare?.dataset ?? '';
		errors = {};
		activeTab = 'details';
	}
	async function save(confirmed = false) {
		if (saving || !validate()) return;
		if (((form.everyone && !form.readOnly) || preservesRoot) && !confirmed) {
			confirmOpen = true;
			return;
		}
		saving = true;
		try {
			const result = await saveNFSShare(
				initialShare?.id ?? null,
				{
					dataset: datasetGUID,
					description: form.description.trim(),
					enabled: form.enabled,
					readOnly: form.readOnly,
					everyone: form.everyone,
					clients: form.everyone ? [] : clients,
					maprootUser: form.maprootUser.trim(),
					maprootGroup: form.maprootGroup.trim(),
					mapallUser: form.mapallUser.trim(),
					mapallGroup: form.mapallGroup.trim(),
					allDirs: form.allDirs,
					security: security as NFSShare['security']
				},
				{ hostname: targetNode }
			);
			if (!mounted) return;
			if (result.status === 'error') {
				const detail = NFSValidationErrorSchema.safeParse(result.data);
				if (detail.success && Object.hasOwn(FIELD_TABS, detail.data.field)) {
					const field = detail.data.field as ErrorField;
					errors[field] = detail.data.message;
					activeTab = FIELD_TABS[field];
				}
				handleAPIError(result);
				return;
			}
			const mutation = NFSMutationSchema.safeParse(result.data);
			if (mutation.success && mutation.data.applyStatus === 'stopped') {
				toast.success('NFS share saved', { position: 'bottom-center' });
			} else if (mutation.success && mutation.data.applied) {
				toast.success('NFS share applied', { position: 'bottom-center' });
			} else {
				toast.warning('NFS share saved; apply pending.', {
					position: 'bottom-center'
				});
			}
			reload = true;
			open = false;
		} finally {
			if (mounted) saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[min(30rem,90dvh)] flex-col overflow-hidden sm:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={initialShare !== null && !saving}
		onReset={reset}
		onClose={() => {
			if (!saving) open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		onInteractOutside={(event) => {
			if (saving) event.preventDefault();
		}}
	>
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--folder-network]"
					size="h-5 w-5"
					gap="gap-2"
					title={initialShare ? 'Edit NFS Share' : 'Create NFS Share'}
				/></Dialog.Title
			>
		</Dialog.Header>
		<Tabs.Root bind:value={activeTab} class="flex min-h-0 flex-1 flex-col gap-1">
			<Tabs.List class="grid w-full shrink-0 grid-cols-2">
				<Tabs.Trigger value="details">Details</Tabs.Trigger>
				<Tabs.Trigger value="access">Access</Tabs.Trigger>
			</Tabs.List>
			{#if tabError(activeTab)}<p
					class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
					role="alert"
				>
					{tabError(activeTab)}
				</p>{/if}
			<Tabs.Content value="details" class="m-0 min-h-0 flex-1 overflow-y-auto py-2">
				<div class="flex min-h-full flex-col gap-4 pb-1">
					<ComboBox
						label="Filesystem"
						bind:open={datasetOpen}
						bind:value={datasetValue}
						data={datasetOptions}
						placeholder="Select a ZFS filesystem"
						width="w-full"
						disabled={saving || datasetsLoading}
					/>
					{#if datasetsError}<p class="text-destructive text-xs" role="alert">
							{datasetsError}
						</p>{/if}
					{#if dataset && !dataset.available}<p class="text-destructive text-xs" role="alert">
							{dataset.reason}
						</p>{/if}
					<CustomValueInput
						label="Description"
						placeholder="Optional"
						type="textarea"
						textAreaClasses="h-24 min-h-24 max-h-24 resize-none"
						bind:value={form.description}
						disabled={saving}
					/>
					<div class="pt-2">
						<CustomCheckbox label="Enabled" bind:checked={form.enabled} disabled={saving} />
					</div>
				</div>
			</Tabs.Content>
			<Tabs.Content value="access" class="m-0 min-h-0 flex-1 overflow-y-auto py-2">
				<div class="flex min-h-full flex-col gap-3 pb-1">
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<ComboBox
							label="Security"
							multiple
							bind:open={securityOpen}
							bind:value={security}
							data={[
								{ value: 'sys', label: 'SYS' },
								{ value: 'krb5', label: 'KRB5' },
								{ value: 'krb5i', label: 'KRB5I' },
								{ value: 'krb5p', label: 'KRB5P' }
							]}
							placeholder="Select security flavors"
							triggerWidth="flex w-full"
							buttonClass="h-9 min-h-9 py-1"
							width="w-full"
							disabled={saving}
						/>
						<ComboBoxBindable
							label="Authorized Clients"
							placeholder="IP address or CIDR"
							multiple
							bind:open={form.clients.open}
							bind:value={form.clients.value}
							bind:data={form.clients.options}
							classes="space-y-1"
							labelExtraClasses="h-7"
							buttonClass="h-9 min-h-9 py-1"
							width="w-full"
							disabled={saving || form.everyone}
						/>
					</div>
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<CustomValueInput
							label="Maproot User"
							placeholder="Default"
							bind:value={form.maprootUser}
							disabled={saving}
						/>
						<CustomValueInput
							label="Maproot Group"
							placeholder="Default"
							bind:value={form.maprootGroup}
							disabled={saving}
						/>
						<CustomValueInput
							label="Mapall User"
							placeholder="None"
							bind:value={form.mapallUser}
							disabled={saving}
						/>
						<CustomValueInput
							label="Mapall Group"
							placeholder="Default"
							bind:value={form.mapallGroup}
							disabled={saving}
						/>
					</div>
					<div class="grid grid-cols-1 gap-2 pt-3 sm:grid-cols-[repeat(3,max-content)] sm:gap-x-4">
						<CustomCheckbox label="Read-only" bind:checked={form.readOnly} disabled={saving} />
						<CustomCheckbox label="All Directories" bind:checked={form.allDirs} disabled={saving} />
						<CustomCheckbox label="Everyone" bind:checked={form.everyone} disabled={saving} />
					</div>
				</div>
			</Tabs.Content>
		</Tabs.Root>
		<Dialog.Footer class="shrink-0"
			><Button
				size="sm"
				disabled={saving || (datasetsLoading && needsDataset)}
				onclick={() => save()}
				>{saving ? 'Saving...' : initialShare ? 'Save Changes' : 'Create Share'}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
<AlertDialog
	bind:open={confirmOpen}
	description={preservesRoot
		? 'Allow remote root-level access to this share?'
		: 'Allow everyone to read and write this share?'}
	confirmLabel="Save"
	keepOpenOnConfirm
	loading={saving}
	actions={{
		onConfirm: async () => {
			confirmOpen = false;
			await save(true);
		},
		onCancel: () => (confirmOpen = false)
	}}
/>
