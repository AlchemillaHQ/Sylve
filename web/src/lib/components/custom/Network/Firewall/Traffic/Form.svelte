<script lang="ts">
	import { createFirewallTrafficRule, updateFirewallTrafficRule } from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { FirewallTrafficRule } from '$lib/types/network/firewall';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { handleAPIError } from '$lib/utils/http';
	import { validateFirewallTrafficRulePayload } from '$lib/utils/network/firewall';
	import { toast } from 'svelte-sonner';
	import { SvelteSet } from 'svelte/reactivity';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		trafficRules: FirewallTrafficRule[];
		objects: NetworkObject[];
		interfaces: Iface[];
		switches: SwitchList;
		wgClients?: WireGuardClient[];
		afterChange: () => void;
	}

	let {
		open = $bindable(),
		edit = false,
		id,
		trafficRules,
		objects,
		interfaces,
		switches,
		wgClients = [],
		afterChange
	}: Props = $props();

	const editingRule = $derived.by(() => {
		if (edit && id) return trafficRules.find((r) => r.id === id) ?? null;
		return null;
	});

	// Resolve: if value matches an addr object ID → use objId, else → use raw string
	function resolveAddr(val: string): { raw: string; objId: number | null } {
		const trimmed = val.trim();
		if (!trimmed) return { raw: '', objId: null };
		const obj = objects.find(
			(o) => ['Host', 'Network', 'FQDN', 'List'].includes(o.type) && String(o.id) === trimmed
		);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	function resolvePort(val: string): { raw: string; objId: number | null } {
		const trimmed = val.trim();
		if (!trimmed) return { raw: '', objId: null };
		const obj = objects.find((o) => o.type === 'Port' && String(o.id) === trimmed);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	// For populating form when editing: object ID takes priority over raw string
	function addrToForm(raw: string | undefined, objId: number | null | undefined): string {
		if (objId) return String(objId);
		return raw ?? '';
	}

	type Form = {
		name: string;
		description: string;
		enabled: boolean;
		log: boolean;
		quick: boolean;
		priority: number;
		action: string;
		direction: string;
		protocol: string;
		family: string;
		ingressInterfaces: string[];
		egressInterfaces: string[];
		source: string;
		dest: string;
		srcPort: string;
		dstPort: string;
	};

	function defaultForm(): Form {
		return {
			name: '',
			description: '',
			enabled: true,
			log: false,
			quick: false,
			priority: trafficRules.length + 1,
			action: 'pass',
			direction: 'in',
			protocol: 'any',
			family: 'any',
			ingressInterfaces: [],
			egressInterfaces: [],
			source: '',
			dest: '',
			srcPort: '',
			dstPort: ''
		};
	}

	let form = $state(defaultForm());

	$effect(() => {
		if (open) {
			if (editingRule) {
				form = {
					name: editingRule.name,
					description: editingRule.description ?? '',
					enabled: editingRule.enabled ?? true,
					log: editingRule.log ?? false,
					quick: editingRule.quick ?? false,
					priority: editingRule.priority,
					action: editingRule.action,
					direction: editingRule.direction,
					protocol: editingRule.protocol,
					family: editingRule.family ?? 'any',
					ingressInterfaces: editingRule.ingressInterfaces ?? [],
					egressInterfaces: editingRule.egressInterfaces ?? [],
					source: addrToForm(editingRule.sourceRaw, editingRule.sourceObjId),
					dest: addrToForm(editingRule.destRaw, editingRule.destObjId),
					srcPort: addrToForm(editingRule.srcPortsRaw, editingRule.srcPortObjId),
					dstPort: addrToForm(editingRule.dstPortsRaw, editingRule.dstPortObjId)
				};
			} else {
				form = defaultForm();
			}
		}
	});

	let cbOpen = $state({
		ingressInterfaces: false,
		egressInterfaces: false,
		source: false,
		dest: false,
		srcPort: false,
		dstPort: false
	});

	const actionOptions = [
		{ value: 'pass', label: 'Pass' },
		{ value: 'block', label: 'Block' }
	];
	const directionOptions = [
		{ value: 'in', label: 'In' },
		{ value: 'out', label: 'Out' }
	];
	const protocolOptions = [
		{ value: 'any', label: 'Any' },
		{ value: 'tcp', label: 'TCP' },
		{ value: 'udp', label: 'UDP' },
		{ value: 'icmp', label: 'ICMP' }
	];
	const familyOptions = [
		{ value: 'any', label: 'Any' },
		{ value: 'inet', label: 'IPv4' },
		{ value: 'inet6', label: 'IPv6' }
	];

	const ifaceOptions = $derived.by(() => {
		const opts: { label: string; value: string }[] = [];
		const coveredValues = new SvelteSet<string>();

		for (const sw of switches.standard ?? []) {
			opts.push({ label: sw.name, value: sw.bridgeName });
			coveredValues.add(sw.bridgeName);
		}
		for (const sw of switches.manual ?? []) {
			opts.push({ label: sw.name, value: sw.bridge });
			coveredValues.add(sw.bridge);
		}
		for (const iface of interfaces) {
			if (coveredValues.has(iface.name)) continue;
			let label = iface.description || iface.name;
			if (iface.name === 'wgs0') {
				label = 'WireGuard Server';
			} else {
				const wgcMatch = iface.name.match(/^wgc(\d+)$/);
				if (wgcMatch) {
					const clientId = Number(wgcMatch[1]);
					const client = wgClients.find((c) => c.id === clientId);
					if (client) label = `${client.name} (WG Client)`;
				}
			}
			opts.push({ label, value: iface.name });
		}
		return opts;
	});

	// Address objects as combobox options — allowCustom lets users type raw IPs/CIDRs inline
	const addrObjectOptions = $derived(
		objects
			.filter((obj) => ['Host', 'Network', 'FQDN', 'List'].includes(obj.type))
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	// Port objects — allowCustom lets users type raw ports inline
	const portObjectOptions = $derived(
		objects
			.filter((obj) => obj.type === 'Port')
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const showPorts = $derived(form.protocol === 'tcp' || form.protocol === 'udp');
	const isInbound = $derived(form.direction === 'in');
	const isOutbound = $derived(form.direction === 'out');

	async function save() {
		if (!form.name.trim()) {
			toast.error('Rule name is required', { position: 'bottom-center' });
			return;
		}

		const src = resolveAddr(form.source);
		const dst = resolveAddr(form.dest);
		const sp = showPorts ? resolvePort(form.srcPort) : { raw: '', objId: null };
		const dp = showPorts ? resolvePort(form.dstPort) : { raw: '', objId: null };

		const payload = {
			name: form.name.trim(),
			description: form.description.trim(),
			enabled: form.enabled,
			log: form.log,
			quick: form.quick,
			priority: Number(form.priority),
			action: form.action as 'pass' | 'block',
			direction: form.direction as 'in' | 'out',
			protocol: form.protocol as 'any' | 'tcp' | 'udp' | 'icmp',
			family: form.family as 'any' | 'inet' | 'inet6',
			ingressInterfaces: isInbound ? form.ingressInterfaces : [],
			egressInterfaces: isOutbound ? form.egressInterfaces : [],
			sourceRaw: src.raw,
			sourceObjId: src.objId,
			destRaw: dst.raw,
			destObjId: dst.objId,
			srcPortsRaw: sp.raw,
			srcPortObjId: sp.objId,
			dstPortsRaw: dp.raw,
			dstPortObjId: dp.objId
		};

		const validation = validateFirewallTrafficRulePayload(payload);
		if (!validation.valid) {
			toast.error(validation.error ?? 'Invalid traffic rule', { position: 'bottom-center' });
			return;
		}

		const result =
			edit && id
				? await updateFirewallTrafficRule(id, payload)
				: await createFirewallTrafficRule(payload);

		if (typeof result === 'number' || ('status' in result && result.status === 'success')) {
			toast.success(`Traffic rule ${edit ? 'updated' : 'created'}`, { position: 'bottom-center' });
			afterChange();
			open = false;
			form = defaultForm();
		} else {
			handleAPIError(result);
			toast.error(`Failed to ${edit ? 'update' : 'create'} traffic rule`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[96%] overflow-hidden p-5 lg:max-w-3xl md:max-w-2xl">
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--shield-check-outline] h-5 w-5"></span>
						{#if editingRule}
							<span>Edit Rule — {editingRule.name}</span>
						{:else}
							<span>Create Traffic Rule</span>
						{/if}
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="link"
					class="h-4"
					title="Reset"
					onclick={() => (form = defaultForm())}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Reset</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title="Close" onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</div>
		</div>

		<ScrollArea orientation="vertical" class="h-[68vh] pr-2">
			<div class="space-y-5">
				<section>
					<div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
						<CustomValueInput
							label="Name"
							placeholder="Block External Traffic"
							bind:value={form.name}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Description"
							placeholder="Optional description"
							bind:value={form.description}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Priority"
							placeholder="1"
							type="number"
							bind:value={form.priority}
							classes="space-y-1.5"
						/>
					</div>
				</section>

				<div class="border-t"></div>

				<!-- Rule Settings -->
				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Rule Settings
					</p>
					<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
						<SimpleSelect
							label="Action"
							options={actionOptions}
							bind:value={form.action}
							onChange={(v) => (form.action = v)}
						/>
						<SimpleSelect
							label="Direction"
							options={directionOptions}
							bind:value={form.direction}
							onChange={(v) => (form.direction = v)}
						/>
						<SimpleSelect
							label="Protocol"
							options={protocolOptions}
							bind:value={form.protocol}
							onChange={(v) => (form.protocol = v)}
						/>
						<SimpleSelect
							label="Family"
							options={familyOptions}
							bind:value={form.family}
							onChange={(v) => (form.family = v)}
						/>
					</div>
				</section>

				<div class="border-t"></div>

				<!-- Interfaces -->
				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Interfaces
					</p>
					{#if interfaces.length > 0}
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<ComboBox
								bind:open={cbOpen.ingressInterfaces}
								label="Ingress Interfaces"
								bind:value={form.ingressInterfaces}
								data={ifaceOptions}
								classes="space-y-1"
								placeholder={isOutbound ? 'Not used for outbound rules' : 'Any ingress interface'}
								disabled={isOutbound}
								width="w-full"
								multiple={true}
							/>
							<ComboBox
								bind:open={cbOpen.egressInterfaces}
								label="Egress Interfaces"
								bind:value={form.egressInterfaces}
								data={ifaceOptions}
								classes="space-y-1"
								placeholder={isInbound ? 'Not used for inbound rules' : 'Any egress interface'}
								disabled={isInbound}
								width="w-full"
								multiple={true}
							/>
						</div>
					{:else}
						<p class="text-xs text-muted-foreground">
							No interfaces available — rule matches on any interface direction.
						</p>
					{/if}
				</section>

				<div class="border-t"></div>

				<!-- Source & Destination -->
				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Source & Destination
					</p>
					<p class="mb-3 text-xs text-muted-foreground">
						Select a network object or type a raw IP / CIDR, leave empty to match any.
					</p>
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<ComboBox
							bind:open={cbOpen.source}
							label="Source"
							bind:value={form.source}
							data={addrObjectOptions}
							classes="space-y-1"
							placeholder="any - object or 192.168.1.0/24"
							width="w-full"
							allowCustom={true}
						/>
						<ComboBox
							bind:open={cbOpen.dest}
							label="Destination"
							bind:value={form.dest}
							data={addrObjectOptions}
							classes="space-y-1"
							placeholder="any - object or 10.0.0.0/8"
							width="w-full"
							allowCustom={true}
						/>
					</div>
				</section>

				{#if showPorts}
					<div class="border-t"></div>

					<!-- Ports -->
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							Ports
						</p>
						<p class="mb-3 text-xs text-muted-foreground">
							Select a port object or type raw ports (e.g. 80, 443, 8000:9000). Leave empty for any.
						</p>
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<ComboBox
								bind:open={cbOpen.srcPort}
								label="Source Ports"
								bind:value={form.srcPort}
								data={portObjectOptions}
								classes="space-y-1"
								placeholder="any - object or 1024:65535"
								width="w-full"
								allowCustom={true}
							/>
							<ComboBox
								bind:open={cbOpen.dstPort}
								label="Destination Ports"
								bind:value={form.dstPort}
								data={portObjectOptions}
								classes="space-y-1"
								placeholder="any - object or 80, 443"
								width="w-full"
								allowCustom={true}
							/>
						</div>
					</section>
				{/if}

				<div class="mt-3 flex flex-row gap-4">
					<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
					<CustomCheckbox label="Log" bind:checked={form.log} />
					<CustomCheckbox label="Quick" bind:checked={form.quick} />
				</div>
			</div>
		</ScrollArea>

		<Dialog.Footer class="pt-2">
			<div class="flex items-center gap-2">
				<Button size="sm" variant="outline" onclick={() => (open = false)}>Cancel</Button>
				<Button size="sm" onclick={save}>{edit ? 'Save' : 'Create'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
