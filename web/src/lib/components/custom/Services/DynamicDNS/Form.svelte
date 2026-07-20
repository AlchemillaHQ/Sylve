<script lang="ts">
	import { createDynamicDNSEntry, updateDynamicDNSEntry } from '$lib/api/services/dynamic-dns';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { DynamicDNSEntry, DynamicDNSEntryInput } from '$lib/types/services/dynamic-dns';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		entries: DynamicDNSEntry[];
		interfaces: Iface[];
		switches?: SwitchList;
		afterChange: () => void;
	}

	let {
		open = $bindable(),
		edit = false,
		id,
		entries,
		interfaces,
		switches = {},
		afterChange
	}: Props = $props();

	const editingEntry = $derived.by(() =>
		edit && id ? (entries.find((entry) => entry.id === id) ?? null) : null
	);
	const isEditing = $derived(editingEntry !== null);

	type Form = {
		enabled: boolean;
		provider: DynamicDNSEntryInput['provider'];
		hostname: string;
		recordType: string;
		intervalMinutes: number;
		token: string;
		namecheapDomain: string;
		sourceType: string;
		interfaceName: string;
		manualIPv4: string;
		manualIPv6: string;
		stunServer: string;
	};

	const recordTypeOptions = [
		{ value: 'A', label: 'IPv4 (A)' },
		{ value: 'AAAA', label: 'IPv6 (AAAA)' },
		{ value: 'BOTH', label: 'IPv4 + IPv6' }
	];
	const providerOptions = [
		{ value: 'cloudflare', label: 'Cloudflare' },
		{ value: 'namecheap', label: 'Namecheap' }
	];
	const sourceTypeOptions = [
		{ value: 'stun', label: 'STUN' },
		{ value: 'interface', label: 'Interface' },
		{ value: 'manual', label: 'Manual' }
	];
	const selectClasses = {
		parent: 'min-w-0 space-y-1.5',
		label: 'flex h-7 items-center whitespace-nowrap text-sm',
		trigger:
			'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 py-1 text-left'
	};
	const fullWidthSelectClasses = {
		...selectClasses,
		parent: 'min-w-0 space-y-1.5 sm:col-span-2'
	};

	function isSelectableInterface(iface: Iface): boolean {
		return (
			!/^lo\d+$/.test(iface.name) &&
			!iface.name.startsWith('epair') &&
			!iface.groups?.includes('epair')
		);
	}

	function interfaceLabel(iface: Iface): string {
		if (iface.name === 'wgs0') return `WireGuard Server (${iface.name})`;

		const switchName =
			switches.manual?.find((networkSwitch) => networkSwitch.bridge === iface.name)?.name ??
			switches.standard?.find((networkSwitch) => networkSwitch.bridgeName === iface.name)?.name;
		if (switchName) return `${switchName} (${iface.name})`;

		const description = iface.description.trim();
		return description ? `${description} (${iface.name})` : iface.name;
	}

	const interfaceOptions = $derived.by(() =>
		[...interfaces]
			.filter(isSelectableInterface)
			.sort((first, second) => first.name.localeCompare(second.name))
			.map((iface) => ({ value: iface.name, label: interfaceLabel(iface) }))
	);

	function defaultForm(): Form {
		return {
			enabled: true,
			provider: 'cloudflare',
			hostname: '',
			recordType: 'A',
			intervalMinutes: 10,
			token: '',
			namecheapDomain: '',
			sourceType: 'stun',
			interfaceName: '',
			manualIPv4: '',
			manualIPv6: '',
			stunServer: 'stun.l.google.com:19302'
		};
	}

	function entryForm(entry: DynamicDNSEntry): Form {
		return {
			enabled: entry.enabled,
			provider: entry.provider as DynamicDNSEntryInput['provider'],
			hostname: entry.hostname,
			recordType: entry.recordType,
			intervalMinutes: entry.intervalMinutes,
			token: '',
			namecheapDomain: entry.providerSettings.domain ?? '',
			sourceType: entry.sourceType,
			interfaceName: entry.sourceSettings.interface ?? '',
			manualIPv4: entry.sourceSettings.ipv4 ?? '',
			manualIPv6: entry.sourceSettings.ipv6 ?? '',
			stunServer: entry.sourceSettings.server ?? 'stun.l.google.com:19302'
		};
	}

	// svelte-ignore state_referenced_locally
	let form = $state<Form>(editingEntry ? entryForm(editingEntry) : defaultForm());
	let interfaceOpen = $state(false);
	let saving = $state(false);
	const credentialLabel = $derived(
		form.provider === 'namecheap' ? 'Namecheap Dynamic DNS Password' : 'Cloudflare API Token'
	);
	const credentialPlaceholder = $derived(
		!isEditing || editingEntry?.provider !== form.provider
			? form.provider === 'namecheap'
				? 'Paste Dynamic DNS password'
				: 'Paste API token'
			: form.provider === 'namecheap'
				? 'Leave blank to keep the configured password'
				: 'Leave blank to keep the configured token'
	);
	const credentialRequired = $derived(!isEditing || editingEntry?.provider !== form.provider);

	function resetForm() {
		form = editingEntry ? entryForm(editingEntry) : defaultForm();
	}

	function sourceSettings(): Record<string, string> {
		switch (form.sourceType) {
			case 'interface':
				return { interface: form.interfaceName };
			case 'manual':
				return { ipv4: form.manualIPv4, ipv6: form.manualIPv6 };
			default:
				return { server: form.stunServer };
		}
	}

	function providerSettings(): Record<string, string> {
		if (form.provider === 'namecheap') {
			return { domain: form.namecheapDomain.trim() };
		}
		return editingEntry?.provider === 'cloudflare' ? { ...editingEntry.providerSettings } : {};
	}

	function selectProvider(value: string) {
		form.provider = value as DynamicDNSEntryInput['provider'];
		if (form.provider === 'namecheap') form.recordType = 'A';
	}

	function validManualSource() {
		if (form.recordType === 'A') return Boolean(form.manualIPv4.trim());
		if (form.recordType === 'AAAA') return Boolean(form.manualIPv6.trim());
		return Boolean(form.manualIPv4.trim() || form.manualIPv6.trim());
	}

	async function save() {
		if (!form.hostname.trim()) {
			toast.error('Hostname is required', { position: 'bottom-center' });
			return;
		}
		const intervalMinutes = Number(form.intervalMinutes);
		if (!Number.isInteger(intervalMinutes) || intervalMinutes < 1 || intervalMinutes > 1440) {
			toast.error('Update interval must be a whole number between 1 and 1440 minutes', {
				position: 'bottom-center'
			});
			return;
		}
		if (credentialRequired && !form.token.trim()) {
			toast.error(`${credentialLabel} is required`, { position: 'bottom-center' });
			return;
		}
		if (form.provider === 'namecheap' && !form.namecheapDomain.trim()) {
			toast.error('Namecheap domain is required', { position: 'bottom-center' });
			return;
		}
		if (form.sourceType === 'interface' && !form.interfaceName) {
			toast.error('Select a network interface', { position: 'bottom-center' });
			return;
		}
		if (form.sourceType === 'manual' && !validManualSource()) {
			toast.error('Provide an address for the selected record type', { position: 'bottom-center' });
			return;
		}

		const input: DynamicDNSEntryInput = {
			enabled: form.enabled,
			provider: form.provider,
			providerSettings: providerSettings(),
			...(form.token.trim() ? { token: form.token.trim() } : {}),
			hostname: form.hostname.trim(),
			recordType: form.recordType as DynamicDNSEntryInput['recordType'],
			intervalMinutes,
			sourceType: form.sourceType as DynamicDNSEntryInput['sourceType'],
			sourceSettings: sourceSettings()
		};

		saving = true;
		const result = editingEntry
			? await updateDynamicDNSEntry(editingEntry.id, input)
			: await createDynamicDNSEntry(input);
		saving = false;

		if (isAPIResponse(result)) {
			handleAPIError(result);
			toast.error(`Failed to ${isEditing ? 'update' : 'create'} Dynamic DNS entry`, {
				position: 'bottom-center'
			});
			return;
		}

		toast.success(`Dynamic DNS entry ${isEditing ? 'updated' : 'created'}`, {
			position: 'bottom-center'
		});
		afterChange();
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] overflow-hidden p-5 md:max-w-2xl lg:max-w-3xl"
		showCloseButton={true}
		showResetButton={true}
		onReset={resetForm}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--dns]"
					size="h-5 w-5"
					gap="gap-2"
					title={editingEntry ? `Edit ${editingEntry.hostname}` : 'Add Dynamic DNS Entry'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<ScrollArea orientation="vertical" class="max-h-[56vh] pr-2">
			<div class="space-y-5">
				<section class="grid grid-cols-1 gap-4 sm:grid-cols-2">
					<SimpleSelect
						label="Provider"
						options={providerOptions}
						classes={selectClasses}
						bind:value={form.provider}
						onChange={selectProvider}
					/>
					<CustomValueInput
						label="Hostname"
						placeholder="home.example.com"
						bind:value={form.hostname}
						classes="space-y-1.5"
					/>
					<SimpleSelect
						label="Record Type"
						options={recordTypeOptions}
						classes={selectClasses}
						bind:value={form.recordType}
						onChange={(value) => (form.recordType = value)}
						disabled={form.provider === 'namecheap'}
					/>
					<CustomValueInput
						label="Interval (minutes)"
						placeholder="10"
						type="number"
						hint="Whole minutes, from 1 to 1440."
						bind:value={form.intervalMinutes}
						classes="space-y-1.5"
					/>
				</section>

				<section class="grid grid-cols-1 gap-4 sm:grid-cols-2">
					{#if form.provider === 'namecheap'}
						<CustomValueInput
							label="Namecheap Domain"
							placeholder="example.com"
							hint="Use the exact domain name shown in your Namecheap account."
							bind:value={form.namecheapDomain}
							classes="space-y-1.5"
						/>
					{/if}
					<CustomValueInput
						label={credentialLabel}
						placeholder={credentialPlaceholder}
						type="password"
						revealOnFocus={true}
						bind:value={form.token}
						classes={form.provider === 'namecheap' ? 'space-y-1.5' : 'space-y-1.5 sm:col-span-2'}
					/>
				</section>

				<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
					<SimpleSelect
						label="Source"
						options={sourceTypeOptions}
						classes={form.sourceType === 'manual' ? fullWidthSelectClasses : selectClasses}
						bind:value={form.sourceType}
						onChange={(value) => (form.sourceType = value)}
					/>

					{#if form.sourceType === 'interface'}
						<ComboBox
							bind:open={interfaceOpen}
							label="Interface"
							bind:value={form.interfaceName}
							data={interfaceOptions}
							placeholder="Select interface"
							width="w-full"
							classes="space-y-1.5"
							buttonClass="h-9 px-3 py-1"
						/>
					{:else if form.sourceType === 'stun'}
						<CustomValueInput
							label="STUN Server"
							placeholder="stun.l.google.com:19302"
							bind:value={form.stunServer}
							classes="space-y-1.5"
						/>
					{/if}

					{#if form.sourceType === 'manual'}
						<CustomValueInput
							label="IPv4 Address"
							placeholder="203.0.113.10"
							bind:value={form.manualIPv4}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="IPv6 Address"
							placeholder="2001:db8::10"
							bind:value={form.manualIPv6}
							classes="space-y-1.5"
						/>
					{/if}
				</div>

				<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
			</div>
		</ScrollArea>

		<Dialog.Footer class="pt-2">
			<div class="flex items-center gap-2">
				<Button size="sm" onclick={save} disabled={saving}>
					{saving ? 'Saving...' : isEditing ? 'Save' : 'Create'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
