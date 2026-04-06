<script lang="ts">
	import { createStaticRoute, updateStaticRoute } from '$lib/api/network/route';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { Iface } from '$lib/types/network/iface';
	import type { StaticRoute } from '$lib/types/network/route';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError } from '$lib/utils/http';
	import { validateStaticRoutePayload } from '$lib/utils/network/route';
	import { toast } from 'svelte-sonner';
	import { SvelteSet } from 'svelte/reactivity';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		routes: StaticRoute[];
		interfaces: Iface[];
		objects?: NetworkObject[];
		switches?: SwitchList;
		prefill?: Partial<StaticRoute> | null;
		suggestions?: Partial<StaticRoute>[];
		afterChange: () => void;
	}

	let {
		open = $bindable(),
		edit = false,
		id,
		routes,
		interfaces,
		objects = [] as NetworkObject[],
		switches = { standard: [], manual: [] } as SwitchList,
		prefill = null,
		suggestions = [],
		afterChange
	}: Props = $props();

	type Form = {
		name: string;
		description: string;
		enabled: boolean;
		fib: number;
		destinationType: 'host' | 'network';
		destination: string;
		family: 'inet' | 'inet6';
		nextHopMode: 'gateway' | 'interface';
		gateway: string;
		interface: string;
	};

	function defaultForm(): Form {
		return {
			name: '',
			description: '',
			enabled: true,
			fib: 0,
			destinationType: 'network',
			destination: '',
			family: 'inet',
			nextHopMode: 'interface',
			gateway: '',
			interface: ''
		};
	}

	const editingRoute = $derived.by(() => {
		if (edit && id) {
			return routes.find((route) => route.id === id) ?? null;
		}
		return null;
	});

	let form = $state(defaultForm());
	let cbOpen = $state({
		iface: false,
		destination: false,
		gateway: false
	});
	let selectedSuggestion = $state('0');

	function applySuggestion(prefillRoute: Partial<StaticRoute>) {
		form = {
			name: String(prefillRoute.name ?? ''),
			description: String(prefillRoute.description ?? ''),
			enabled: Boolean(prefillRoute.enabled ?? true),
			fib: Number(prefillRoute.fib ?? 0),
			destinationType: (prefillRoute.destinationType === 'host' ? 'host' : 'network') as
				| 'host'
				| 'network',
			destination: String(prefillRoute.destination ?? ''),
			family: (prefillRoute.family === 'inet6' ? 'inet6' : 'inet') as 'inet' | 'inet6',
			nextHopMode: (prefillRoute.nextHopMode === 'gateway' ? 'gateway' : 'interface') as
				| 'gateway'
				| 'interface',
			gateway: String(prefillRoute.gateway ?? ''),
			interface: String(prefillRoute.interface ?? '')
		};
	}

	watch(
		() => open,
		(isOpen) => {
			if (!isOpen) return;

			if (editingRoute) {
				form = {
					name: editingRoute.name,
					description: editingRoute.description ?? '',
					enabled: editingRoute.enabled ?? true,
					fib: editingRoute.fib ?? 0,
					destinationType: editingRoute.destinationType,
					destination: editingRoute.destination,
					family: editingRoute.family,
					nextHopMode: editingRoute.nextHopMode,
					gateway: editingRoute.gateway ?? '',
					interface: editingRoute.interface ?? ''
				};
				return;
			}

			if (prefill) {
				applySuggestion(prefill);
				return;
			}

			form = defaultForm();
		}
	);

	watch(
		() => selectedSuggestion,
		(val) => {
			if (!open || !suggestions || suggestions.length <= 1 || editingRoute) return;
			const idx = Number.parseInt(val, 10);
			if (!Number.isFinite(idx) || idx < 0 || idx >= suggestions.length) {
				selectedSuggestion = '0';
				applySuggestion(suggestions[0]);
				return;
			}
			applySuggestion(suggestions[idx]);
		}
	);

	watch(
		() => form.nextHopMode,
		(mode) => {
			if (mode === 'gateway') {
				form.interface = '';
			} else {
				form.gateway = '';
			}
		}
	);

	const familyOptions = [
		{ value: 'inet', label: 'IPv4' },
		{ value: 'inet6', label: 'IPv6' }
	];
	const destinationTypeOptions = [
		{ value: 'network', label: 'Network' },
		{ value: 'host', label: 'Host' }
	];
	const nextHopModeOptions = [
		{ value: 'interface', label: 'Interface' },
		{ value: 'gateway', label: 'Gateway' }
	];
	const suggestionOptions = $derived.by(() => {
		return (suggestions ?? []).map((suggestion, idx) => {
			const destination = String(suggestion.destination ?? '');
			const destinationType = String(suggestion.destinationType ?? 'network').toUpperCase();
			const fib = Number(suggestion.fib ?? 0);
			const nh =
				String(suggestion.nextHopMode ?? '') === 'gateway'
					? `gw:${String(suggestion.gateway ?? '-')}`
					: `if:${String(suggestion.interface ?? '-')}`;
			return {
				value: String(idx),
				label: `${destinationType} ${destination} (fib ${fib}, ${nh})`
			};
		});
	});

	const ifaceOptions = $derived.by(() => {
		const opts: { label: string; value: string }[] = [];
		const covered = new SvelteSet<string>();

		for (const sw of switches.standard ?? []) {
			opts.push({ label: sw.name, value: sw.bridgeName });
			covered.add(sw.bridgeName);
		}
		for (const sw of switches.manual ?? []) {
			opts.push({ label: sw.name, value: sw.bridge });
			covered.add(sw.bridge);
		}
		for (const iface of interfaces) {
			if (covered.has(iface.name)) continue;
			opts.push({ label: iface.description || iface.name, value: iface.name });
		}
		return opts;
	});

	const destObjectOptions = $derived(
		objects
			.filter((obj) => ['Host', 'Network'].includes(obj.type))
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const gatewayObjectOptions = $derived(
		objects
			.filter((obj) => obj.type === 'Host')
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	function resolveObjToRaw(val: string): string {
		const trimmed = val.trim();
		if (!trimmed) return '';
		const obj = objects.find((o) => String(o.id) === trimmed);
		if (obj?.entries && obj.entries.length > 0) {
			return obj.entries[0].value;
		}
		return trimmed;
	}

	async function save() {
		const payload = {
			name: form.name.trim(),
			description: form.description.trim(),
			enabled: form.enabled,
			fib: Number(form.fib),
			destinationType: form.destinationType,
			destination: resolveObjToRaw(form.destination),
			family: form.family,
			nextHopMode: form.nextHopMode,
			gateway: form.nextHopMode === 'gateway' ? resolveObjToRaw(form.gateway) : '',
			interface: form.nextHopMode === 'interface' ? form.interface.trim() : ''
		};

		const validation = validateStaticRoutePayload(payload);
		if (!validation.valid) {
			toast.error(validation.error ?? 'Invalid route', { position: 'bottom-center' });
			return;
		}

		const result =
			edit && id ? await updateStaticRoute(id, payload) : await createStaticRoute(payload);
		if (typeof result === 'number' || ('status' in result && result.status === 'success')) {
			toast.success(`Route ${edit ? 'updated' : 'created'}`, { position: 'bottom-center' });
			afterChange();
			open = false;
			form = defaultForm();
		} else {
			handleAPIError(result);
			toast.error(`Failed to ${edit ? 'update' : 'create'} route`, { position: 'bottom-center' });
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[96%] overflow-hidden p-5 lg:max-w-2xl md:max-w-xl">
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--routes] h-5 w-5"></span>
						{#if editingRoute}
							<span>Edit Route — {editingRoute.name}</span>
						{:else}
							<span>Create Route</span>
						{/if}
					</div>
				</Dialog.Title>
			</Dialog.Header>
			<Button size="sm" variant="link" class="h-4" title="Close" onclick={() => (open = false)}>
				<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
				<span class="sr-only">Close</span>
			</Button>
		</div>

		<ScrollArea orientation="vertical" class="max-h-[70vh] pr-2">
			<div class="space-y-5">
				{#if !editingRoute && suggestions.length > 1}
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							Suggestion
						</p>
						<SimpleSelect
							label=""
							options={suggestionOptions}
							bind:value={selectedSuggestion}
							onChange={(v) => (selectedSuggestion = String(v))}
						/>
					</section>
				{/if}

				<section>
					<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
						<CustomValueInput
							label="Name"
							placeholder="LAN return route"
							bind:value={form.name}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Description"
							placeholder="Optional description"
							bind:value={form.description}
							classes="space-y-1.5"
						/>
					</div>
				</section>

				<section>
					<div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
						<CustomValueInput
							label="FIB"
							type="number"
							placeholder="0"
							bind:value={form.fib}
							classes="space-y-1.5"
						/>
						<SimpleSelect
							label="Type"
							options={destinationTypeOptions}
							bind:value={form.destinationType}
							onChange={(v) => (form.destinationType = v as Form['destinationType'])}
						/>
						<SimpleSelect
							label="Family"
							options={familyOptions}
							bind:value={form.family}
							onChange={(v) => (form.family = v as Form['family'])}
						/>
					</div>
					<div class="mt-3">
						<ComboBox
							bind:open={cbOpen.destination}
							label="Address"
							bind:value={form.destination}
							data={destObjectOptions}
							classes="space-y-1"
							placeholder={form.destinationType === 'host'
								? '192.168.180.102 or object'
								: '192.168.180.0/24 or object'}
							width="w-full"
							allowCustom={true}
						/>
					</div>
				</section>

				<section>
					<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
						<SimpleSelect
							label="Mode"
							options={nextHopModeOptions}
							bind:value={form.nextHopMode}
							onChange={(v) => (form.nextHopMode = v as Form['nextHopMode'])}
						/>
						{#if form.nextHopMode === 'gateway'}
							<ComboBox
								bind:open={cbOpen.gateway}
								label="Gateway"
								bind:value={form.gateway}
								data={gatewayObjectOptions}
								classes="space-y-1"
								placeholder={form.family === 'inet6'
									? '2001:db8::1 or object'
									: '178.63.44.129 or object'}
								width="w-full"
								allowCustom={true}
							/>
						{:else}
							<ComboBox
								bind:open={cbOpen.iface}
								label="Interface"
								bind:value={form.interface}
								data={ifaceOptions}
								classes="space-y-1"
								placeholder="Select interface"
								width="w-full"
								allowCustom={true}
							/>
						{/if}
					</div>
					<div class="mt-3">
						<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
					</div>
				</section>
			</div>
		</ScrollArea>

		<Dialog.Footer>
			<div class="flex items-center gap-2">
				<Button size="sm" variant="outline" onclick={() => (open = false)}>Cancel</Button>
				<Button size="sm" onclick={save}>{edit ? 'Save' : 'Create'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
