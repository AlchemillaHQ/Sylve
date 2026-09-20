<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getHostInterfaceL3 } from '$lib/api/network/ifaceL3';
	import { getNetworkObjects } from '$lib/api/network/object';
	import {
		createSwitch,
		deleteSwitch,
		getSwitches,
		updateSwitch,
		type StandardSwitchMACSource
	} from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import NetworkObjectCreator from '$lib/components/custom/Network/Objects/CreateOrEdit.svelte';
	import StandardSwitchForm from '$lib/components/custom/Network/Switch/Standard/Form.svelte';
	import type {
		StandardSwitchFormState,
		SwitchTab
	} from '$lib/components/custom/Network/Switch/Standard/types';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Iface } from '$lib/types/network/iface';
	import {
		emptyHostInterfaceL3List,
		isHostInterfaceL3List,
		type HostInterfaceL3List
	} from '$lib/types/network/ifaceL3';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		emptySwitchList,
		isSwitchList,
		StandardSwitchRCConflictsSchema,
		type StandardSwitchRCConflict,
		type SwitchList,
		type SwitchRow
	} from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import {
		buildVLANConfig,
		generateTableData,
		type VLANPolicyDraft
	} from '$lib/utils/network/switch/standard';
	import { isValidMTU, isValidVLAN } from '$lib/utils/numbers';
	import {
		escapeHTML,
		isValidIPv4,
		isValidIPv6,
		isValidSwitchName,
		isValidUnicastMACAddress
	} from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[] | APIResponse;
		switches: SwitchList | APIResponse;
		objects: NetworkObject[] | APIResponse;
		hostInterfaceL3: HostInterfaceL3List | APIResponse;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let lastGoodInterfaces = Array.isArray(data.interfaces) ? data.interfaces : ([] as Iface[]);
	// svelte-ignore state_referenced_locally
	let lastGoodNetworkObjects = Array.isArray(data.objects) ? data.objects : ([] as NetworkObject[]);
	// svelte-ignore state_referenced_locally
	let lastGoodSwitches = isSwitchList(data.switches) ? data.switches : emptySwitchList();
	// svelte-ignore state_referenced_locally
	let lastGoodHostInterfaceL3 = isHostInterfaceL3List(data.hostInterfaceL3)
		? data.hostInterfaceL3
		: emptyHostInterfaceL3List();

	const networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodInterfaces;
			}
			lastGoodInterfaces = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodInterfaces }
	);

	const switches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			if (!isSwitchList(res)) {
				handleAPIError(res);
				return lastGoodSwitches;
			}
			lastGoodSwitches = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodSwitches }
	);

	const networkObjects = resource(
		() => 'network-objects',
		async (key) => {
			const res = await getNetworkObjects();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodNetworkObjects;
			}
			lastGoodNetworkObjects = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodNetworkObjects }
	);

	const hostInterfaceL3 = resource(
		() => 'network-interface-l3',
		async (key) => {
			const res = await getHostInterfaceL3();
			if (!isHostInterfaceL3List(res)) {
				handleAPIError(res);
				return lastGoodHostInterfaceL3;
			}
			lastGoodHostInterfaceL3 = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodHostInterfaceL3 }
	);

	let query: string = $state('');
	let useablePorts = $derived.by(() => {
		let available: string[] = [];

		if (networkInterfaces.current) {
			for (const iface of networkInterfaces.current) {
				if (iface.groups?.includes('svm-host-vlan')) continue;
				available.push(iface.name);
			}
		}

		return available.filter((item, index) => available.indexOf(item) === index);
	});

	let configuredHostInterfacePortNames = $derived(
		new Set(
			hostInterfaceL3.current.rows.flatMap((row) =>
				row.vlanParent ? [row.interface, row.vlanParent] : [row.interface]
			)
		)
	);
	let pendingHostInterfacePortNames = $derived(
		new Set(
			hostInterfaceL3.current.targets
				.filter((target) => target.reason === 'host_interface_l3_pending_conflict')
				.map((target) => target.interface)
		)
	);

	function hostInterfacePortOptions(ports: string[], additional?: string[]) {
		return generateComboboxOptions(ports, additional).map((option) => {
			if (pendingHostInterfacePortNames.has(option.value)) {
				return {
					...option,
					label: `${option.label} — Host IP pending`,
					description: 'Confirm or revert the pending Host IP change on the Interfaces page',
					disabled: true
				};
			}
			if (configuredHostInterfacePortNames.has(option.value)) {
				return {
					...option,
					label: `${option.label} — Host IP`,
					description: 'Remove the Host IP configuration on the Interfaces page first',
					disabled: true
				};
			}
			return option;
		});
	}

	let confirmModals = $state<{
		active: '' | 'newSwitch' | 'editSwitch' | 'deleteSwitch';
		newSwitch: StandardSwitchFormState;
		editSwitch: StandardSwitchFormState;
		deleteSwitch: { open: boolean; name: string; id: number };
	}>({
		active: '',
		newSwitch: {
			open: false,
			name: '',
			mtu: '',
			vlan: '',
			disableIPv6: false,
			private: false,
			bridgeMacMode: '' as '' | 'port' | 'object',
			dhcp: false,
			slaac: false,
			defaultRoute: false,
			defaultRoute6: false,
			disableBridgeOffloads: true,
			vlanFiltering: false,
			defaultAccessVlan: '',
			hostVlan: '',
			portPolicies: {} as Record<string, VLANPolicyDraft>
		},
		editSwitch: {
			oldName: '',
			open: false,
			name: '',
			mtu: '',
			vlan: '',
			disableIPv6: false,
			private: false,
			bridgeMacMode: '' as '' | 'port' | 'object',
			dhcp: false,
			slaac: false,
			defaultRoute: false,
			defaultRoute6: false,
			disableBridgeOffloads: false,
			vlanFiltering: false,
			defaultAccessVlan: '',
			hostVlan: '',
			portPolicies: {} as Record<string, VLANPolicyDraft>
		},
		deleteSwitch: {
			open: false,
			name: '',
			id: 0
		}
	});
	let activeTab = $state<SwitchTab>('general');

	let comboBoxes = $state({
		ipv4: {
			open: false,
			value: ''
		},
		ipv4Gw: {
			open: false,
			value: ''
		},
		ipv6: {
			open: false,
			value: ''
		},
		ipv6Gw: {
			open: false,
			value: ''
		},
		ports: {
			open: false,
			value: [] as string[]
		},
		bridgeMacPort: {
			open: false,
			value: ''
		},
		bridgeMacObject: {
			open: false,
			value: ''
		}
	});
	const singleEntryNetworkObjects = $derived(
		networkObjects.current.filter((object) => object.entries?.length === 1)
	);
	const ipv4NetworkOptions = $derived(generateNetworkOptions(singleEntryNetworkObjects, 'IPv4'));
	const ipv4GatewayOptions = $derived(generateIPOptions(networkObjects.current, 'IPv4'));
	const ipv6NetworkOptions = $derived(generateNetworkOptions(singleEntryNetworkObjects, 'IPv6'));
	const ipv6GatewayOptions = $derived(generateIPOptions(networkObjects.current, 'IPv6'));

	const bridgeMACObjects = $derived(
		networkObjects.current.filter(
			(object) =>
				object.type === 'Mac' &&
				object.entries?.length === 1 &&
				object.entries[0] !== undefined &&
				isValidUnicastMACAddress(object.entries[0].value)
		)
	);
	const bridgeMACObjectOptions = $derived(generateMACOptions(bridgeMACObjects));
	const bridgeMACPortOptions = $derived.by(() =>
		comboBoxes.ports.value.map((port) => {
			const interfaceObj = networkInterfaces.current.find((iface) => iface.name === port);
			const mac = interfaceObj?.ether || interfaceObj?.hwaddr || 'Unavailable';
			return { value: port, label: port + ' (' + mac + ')' };
		})
	);
	const effectiveBridgeMAC = $derived.by(() => {
		const mode =
			confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch'
				? confirmModals[confirmModals.active].bridgeMacMode
				: '';
		if (mode === 'port') {
			const interfaceObj = networkInterfaces.current.find(
				(iface) => iface.name === comboBoxes.bridgeMacPort.value
			);
			return interfaceObj?.ether || interfaceObj?.hwaddr || '';
		}
		if (mode === 'object') {
			return (
				bridgeMACObjects.find((object) => object.id.toString() === comboBoxes.bridgeMacObject.value)
					?.entries?.[0]?.value || ''
			);
		}
		return '';
	});

	let bridgeMACObjectModal = $state({
		open: false,
		prefill: { name: '', type: '', value: '' }
	});

	function openBridgeMACObjectCreator() {
		const active = confirmModals[confirmModals.active as 'newSwitch' | 'editSwitch'];
		bridgeMACObjectModal.prefill = {
			name: (active.name.trim() || 'Standard Switch') + ' Bridge MAC',
			type: 'MAC(s)',
			value: ''
		};
		bridgeMACObjectModal.open = true;
	}

	function splitObjectOrManual(
		value: string | string[],
		options: { label: string; value: string }[]
	): { id: number; manual: string } {
		const v = (Array.isArray(value) ? '' : (value ?? '')).trim();
		if (!v) {
			return { id: 0, manual: '' };
		}
		if (options.some((o) => o.value === v)) {
			return { id: Number(v), manual: '' };
		}
		return { id: 0, manual: v };
	}

	function showError(tab: SwitchTab, message: string) {
		activeTab = tab;
		toast.error(message, { position: 'bottom-center' });
	}

	function ensurePortPolicyDrafts(ports: string[]) {
		if (confirmModals.active !== 'newSwitch' && confirmModals.active !== 'editSwitch') return;
		const modal = confirmModals[confirmModals.active];
		for (const port of ports) {
			if (!modal.portPolicies[port]) {
				modal.portPolicies[port] = { mode: '', untaggedVlan: '', taggedVlans: '' };
			}
		}
	}

	let reload = $state(false);
	let saving = $state(false);
	type SwitchConfirmations = {
		addressRemoval: boolean;
		hostLayer3Removal: boolean;
		rcConflicts: boolean;
	};
	const emptyConfirmations = (): SwitchConfirmations => ({
		addressRemoval: false,
		hostLayer3Removal: false,
		rcConflicts: false
	});

	let addressRemovalWarning = $state({
		open: false,
		ports: [] as string[],
		inspectionUnavailable: false,
		confirmations: emptyConfirmations()
	});

	let addressRemovalWarningMessage = $derived.by(() => {
		const portList = addressRemovalWarning.ports
			.map((port) => `<b>${escapeHTML(port)}</b>`)
			.join(', ');
		const noun = addressRemovalWarning.ports.length === 1 ? 'port' : 'ports';
		if (addressRemovalWarning.inspectionUnavailable) {
			return `Sylve could not verify whether the selected ${noun} ${portList} currently ${addressRemovalWarning.ports.length === 1 ? 'has' : 'have'} IP addresses assigned. Continuing will still convert the ${noun} to bridge members and remove their runtime address configuration. Ensure the switch has the replacement management address and that console access is available.`;
		}

		return `The selected ${noun} ${portList} currently ${addressRemovalWarning.ports.length === 1 ? 'has' : 'have'} IP addresses assigned. Continuing will convert the ${noun} to bridge members and remove assigned addresses. If your browser connection uses one of these interfaces, connectivity may be interrupted. Ensure the switch has the replacement management address and that console access is available.`;
	});

	let rcConflictWarning = $state({
		open: false,
		conflicts: [] as StandardSwitchRCConflict[],
		confirmations: emptyConfirmations()
	});

	let hostLayer3RemovalWarning = $state({
		open: false,
		settings: [] as string[],
		confirmations: emptyConfirmations()
	});

	let hostLayer3RemovalWarningMessage = $derived.by(() => {
		const settings = hostLayer3RemovalWarning.settings
			.map((setting) => `<b>${escapeHTML(setting)}</b>`)
			.join(', ');
		return `This switch currently has host networking configured (${settings}). Continuing without a Host VLAN will permanently remove those saved IPv4 and IPv6 settings because a VLAN-filtered bridge base is layer 2 only. If this browser session uses that host connection, connectivity may be lost. Select a Host VLAN to keep host networking, or ensure console access is available before continuing.`;
	});

	function rcConflictDetail(conflict: StandardSwitchRCConflict): string {
		const member = `<b>${escapeHTML(conflict.member || conflict.port || 'selected interface')}</b>`;
		switch (conflict.code) {
			case 'standard_switch_member_rc_l3_conflict': {
				const modes = [
					conflict.dhcp ? 'DHCP' : '',
					conflict.slaac ? 'SLAAC' : '',
					conflict.staticIPv4 ? 'static IPv4' : '',
					conflict.staticIPv6 ? 'static IPv6' : '',
					conflict.aliasesIPv4 ? 'IPv4 aliases' : '',
					conflict.aliasesIPv6 ? 'IPv6 aliases' : ''
				].filter(Boolean);
				return `${member} has external rc.conf layer-3 configuration (${modes.join(', ')}).`;
			}
			case 'standard_switch_member_bridge_ownership_conflict':
				return `${member} is also assigned to bridge <b>${escapeHTML(conflict.conflictingBridge || 'unknown')}</b>.`;
			case 'standard_switch_member_rc_inspection_unavailable':
				return `Sylve could not inspect the effective rc.conf settings for ${member}.`;
			default:
				return 'Sylve could not inspect current runtime bridge ownership.';
		}
	}

	const MAX_VISIBLE_CONFLICTS = 6;

	let rcConflictWarningMessage = $derived.by(() => {
		const visible = rcConflictWarning.conflicts.slice(0, MAX_VISIBLE_CONFLICTS);
		const hiddenCount = rcConflictWarning.conflicts.length - visible.length;
		const details = visible.map((conflict) => `<li>${rcConflictDetail(conflict)}</li>`).join('');
		const hidden =
			hiddenCount > 0 ? `<li>${hiddenCount} more ${hiddenCount === 1 ? 'port' : 'ports'}</li>` : '';
		return `<p>The selected ports have configuration outside Sylve's managed Standard Switches.</p><ul class="mt-2 list-disc space-y-1 pl-5">${details}${hidden}</ul><p class="mt-2">Sylve will not modify those external settings. They may reapply after boot and conflict with the switch at runtime.</p>`;
	});

	function showRCConflictWarning(
		response: APIResponse,
		confirmations: SwitchConfirmations
	): boolean {
		if (response.error !== 'standard_switch_rc_conflicts_require_confirmation') return false;
		const parsed = StandardSwitchRCConflictsSchema.safeParse(response.data);
		if (!parsed.success || parsed.data.length === 0) return false;
		rcConflictWarning.conflicts = parsed.data;
		rcConflictWarning.confirmations = { ...confirmations };
		rcConflictWarning.open = true;
		return true;
	}

	function configuredHostLayer3Settings(row: SwitchRow | null): string[] {
		if (!row) return [];
		const settings: string[] = [];
		if (row.dhcp) settings.push('IPv4 DHCP');
		if (row.networkObj?.id) settings.push(`IPv4 network object “${row.networkObj.name}”`);
		else if (row.networkManual) settings.push(`IPv4 network ${row.networkManual}`);
		if (row.gatewayAddressObj?.id)
			settings.push(`IPv4 gateway object “${row.gatewayAddressObj.name}”`);
		else if (row.gatewayManual) settings.push(`IPv4 gateway ${row.gatewayManual}`);
		if (row.defaultRoute) settings.push('IPv4 default route');
		if (row.slaac) settings.push('IPv6 SLAAC');
		if (row.network6Obj?.id) settings.push(`IPv6 network object “${row.network6Obj.name}”`);
		else if (row.network6Manual) settings.push(`IPv6 network ${row.network6Manual}`);
		if (row.gateway6AddressObj?.id)
			settings.push(`IPv6 gateway object “${row.gateway6AddressObj.name}”`);
		else if (row.gateway6Manual) settings.push(`IPv6 gateway ${row.gateway6Manual}`);
		if (row.defaultRoute6) settings.push('IPv6 default route');
		return settings;
	}

	function selectedBridgeMembers(ports: string[], vlan: number): string[] {
		return ports.map((port) => (vlan > 0 ? `${port}.${vlan}` : port));
	}

	function selectedPortsWithAddresses(
		interfaces: Iface[],
		ports: string[],
		vlan: number
	): string[] {
		const selectedInterfaces = selectedBridgeMembers(ports, vlan);

		return interfaces
			.filter(
				(iface) =>
					selectedInterfaces.includes(iface.name) &&
					((iface.ipv4?.length ?? 0) > 0 || (iface.ipv6?.length ?? 0) > 0)
			)
			.map((iface) => iface.name)
			.sort();
	}

	watch(
		() => reload,
		(current) => {
			if (current) {
				networkInterfaces.refetch();
				switches.refetch();
				networkObjects.refetch();
				reload = false;
			}
		}
	);

	async function confirmAction(confirmations: SwitchConfirmations = emptyConfirmations()) {
		if (saving) return;

		if (confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch') {
			const activeModal = confirmModals[confirmModals.active];
			const normalizedName = activeModal.name.trim();
			if (!isValidSwitchName(normalizedName)) {
				showError('general', 'Invalid switch name');
				return;
			}

			const mtuInput = String(activeModal.mtu ?? '').trim();
			const mtu = mtuInput === '' ? 1500 : Number(mtuInput);
			if (!Number.isInteger(mtu) || !isValidMTU(mtu)) {
				showError('general', 'Invalid MTU');
				return;
			}

			const vlanInput = String(activeModal.vlan ?? '').trim();
			const vlan = vlanInput === '' ? 0 : Number(vlanInput);
			if (!Number.isInteger(vlan) || !isValidVLAN(vlan)) {
				showError('ports', 'Invalid legacy VLAN');
				return;
			}
			if (activeModal.vlanFiltering && vlan !== 0) {
				showError('ports', 'Port VLAN child interfaces cannot be combined with VLAN filtering');
				return;
			}

			ensurePortPolicyDrafts(comboBoxes.ports.value);
			const vlanConfigResult = buildVLANConfig(
				activeModal.vlanFiltering,
				activeModal.defaultAccessVlan,
				activeModal.hostVlan,
				comboBoxes.ports.value,
				activeModal.portPolicies
			);
			if (!vlanConfigResult.ok) {
				showError('ports', vlanConfigResult.error);
				return;
			}
			const vlanConfig = vlanConfigResult.value;
			const hostLayer3Enabled = !activeModal.vlanFiltering || vlanConfig.hostVlan !== null;
			const effectiveDefaultRoute = hostLayer3Enabled && activeModal.defaultRoute;
			const effectiveDefaultRoute6 = hostLayer3Enabled && activeModal.defaultRoute6;

			let bridgeMac: StandardSwitchMACSource;
			if (activeModal.bridgeMacMode === 'port') {
				const sourcePort = comboBoxes.bridgeMacPort.value;
				if (!sourcePort || !comboBoxes.ports.value.includes(sourcePort)) {
					showError('ports', 'Select one of the switch ports as the MAC source');
					return;
				}
				const sourceInterface = networkInterfaces.current.find(
					(iface) => iface.name === sourcePort
				);
				const sourceMAC = sourceInterface?.ether || sourceInterface?.hwaddr || '';
				if (!isValidUnicastMACAddress(sourceMAC)) {
					showError('ports', 'The selected source port does not have a valid unicast MAC address');
					return;
				}
				bridgeMac = { mode: 'port', port: sourcePort };
			} else if (activeModal.bridgeMacMode === 'object') {
				const objectID = Number(comboBoxes.bridgeMacObject.value);
				if (
					!Number.isInteger(objectID) ||
					!bridgeMACObjects.some((object) => object.id === objectID)
				) {
					showError('ports', 'Select a valid single-value MAC object');
					return;
				}
				bridgeMac = { mode: 'object', macObjectId: objectID };
			} else {
				showError('ports', 'Choose how the bridge MAC address is sourced');
				return;
			}

			if (effectiveDefaultRoute) {
				const existingSwitch = switches.current?.standard?.find(
					(sw) =>
						sw.defaultRoute && !(confirmModals.active === 'editSwitch' && sw.id === activeRow?.id)
				);

				if (existingSwitch) {
					showError('ipv4', 'Another switch already owns the IPv4 default route');
					return;
				}
			}

			if (effectiveDefaultRoute6) {
				const existingSwitch = switches.current?.standard?.find(
					(sw) =>
						sw.defaultRoute6 && !(confirmModals.active === 'editSwitch' && sw.id === activeRow?.id)
				);

				if (existingSwitch) {
					showError('ipv6', 'Another switch already owns the IPv6 default route');
					return;
				}
			}

			if (
				confirmModals.active === 'editSwitch' &&
				!hostLayer3Enabled &&
				!confirmations.hostLayer3Removal
			) {
				const settings = configuredHostLayer3Settings(activeRow);
				if (settings.length > 0) {
					hostLayer3RemovalWarning.settings = settings;
					hostLayer3RemovalWarning.confirmations = { ...confirmations };
					hostLayer3RemovalWarning.open = true;
					return;
				}
			}

			const net4 = !hostLayer3Enabled
				? { id: 0, manual: '' }
				: splitObjectOrManual(comboBoxes.ipv4.value, ipv4NetworkOptions);
			const gw4 = !hostLayer3Enabled
				? { id: 0, manual: '' }
				: splitObjectOrManual(comboBoxes.ipv4Gw.value, ipv4GatewayOptions);
			const net6 = !hostLayer3Enabled
				? { id: 0, manual: '' }
				: splitObjectOrManual(comboBoxes.ipv6.value, ipv6NetworkOptions);
			const gw6 = !hostLayer3Enabled
				? { id: 0, manual: '' }
				: splitObjectOrManual(comboBoxes.ipv6Gw.value, ipv6GatewayOptions);

			const manual = {
				network4: net4.manual,
				gateway4: gw4.manual,
				network6: net6.manual,
				gateway6: gw6.manual
			};

			if (manual.network4 && !isValidIPv4(manual.network4, true)) {
				showError('ipv4', 'Invalid IPv4 network — expected CIDR, e.g. 192.168.1.1/24');
				return;
			}
			if (manual.gateway4 && !isValidIPv4(manual.gateway4)) {
				showError('ipv4', 'Invalid IPv4 gateway address');
				return;
			}
			if (manual.network6 && !isValidIPv6(manual.network6, true)) {
				showError('ipv6', 'Invalid IPv6 network — expected CIDR, e.g. 2001:db8::1/64');
				return;
			}
			if (manual.gateway6 && !isValidIPv6(manual.gateway6)) {
				showError('ipv6', 'Invalid IPv6 gateway address');
				return;
			}

			if (comboBoxes.ports.value.length > 0) {
				saving = true;
				let currentInterfaces: Iface[] | APIResponse | undefined;
				try {
					currentInterfaces = await getInterfaces();
				} catch {
					currentInterfaces = undefined;
				} finally {
					saving = false;
				}

				if (!currentInterfaces || isAPIResponse(currentInterfaces)) {
					if (!confirmations.addressRemoval) {
						addressRemovalWarning.ports = selectedBridgeMembers(
							comboBoxes.ports.value,
							vlan
						).sort();
						addressRemovalWarning.confirmations = { ...confirmations };
						addressRemovalWarning.inspectionUnavailable = true;
						addressRemovalWarning.open = true;
						return;
					}
				} else {
					const addressedPorts = selectedPortsWithAddresses(
						currentInterfaces,
						comboBoxes.ports.value,
						vlan
					);
					if (addressedPorts.length > 0 && !confirmations.addressRemoval) {
						addressRemovalWarning.confirmations = { ...confirmations };
						addressRemovalWarning.ports = addressedPorts;
						addressRemovalWarning.inspectionUnavailable = false;
						addressRemovalWarning.open = true;
						return;
					}
				}
			}

			saving = true;
			try {
				const switchConfig = {
					mtu,
					vlan,
					network4: net4.id,
					gateway4: gw4.id,
					network6: net6.id,
					gateway6: gw6.id,
					private: activeModal.private,
					ports: comboBoxes.ports.value,
					bridgeMac,
					disableIPv6: hostLayer3Enabled ? activeModal.disableIPv6 : true,
					slaac: hostLayer3Enabled ? activeModal.slaac : false,
					dhcp: hostLayer3Enabled ? activeModal.dhcp : false,
					defaultRoute: effectiveDefaultRoute,
					defaultRoute6: effectiveDefaultRoute6,
					disableBridgeOffloads: activeModal.disableBridgeOffloads,
					vlanConfig,
					manual,
					confirmHostLayer3Removal: confirmations.hostLayer3Removal,
					confirmRCConflicts: confirmations.rcConflicts
				};
				if (confirmModals.active === 'newSwitch') {
					const created = await createSwitch({ name: normalizedName, ...switchConfig });

					if (isAPIResponse(created)) {
						if (showRCConflictWarning(created, confirmations)) return;
						handleAPIError(created);
						toast.error('Error creating switch', { position: 'bottom-center' });
						return;
					}

					toast.success(`Switch ${normalizedName} created`, {
						position: 'bottom-center'
					});
				} else {
					const edited = await updateSwitch(activeRow?.id as number, switchConfig);

					if (edited.status !== 'success') {
						if (showRCConflictWarning(edited, confirmations)) return;
						if (edited.error === 'standard_switch_host_layer3_removal_requires_confirmation') {
							const settings = configuredHostLayer3Settings(activeRow);
							hostLayer3RemovalWarning.settings =
								settings.length > 0 ? settings : ['saved host IPv4/IPv6 configuration'];
							hostLayer3RemovalWarning.confirmations = { ...confirmations };
							hostLayer3RemovalWarning.open = true;
							return;
						}
						if (
							edited.error ===
							'standard_switch_vlan_filtering_change_requires_no_attached_workloads'
						) {
							showError(
								'ports',
								'Detach every VM and jail network interface from this switch before changing VLAN filtering'
							);
							return;
						}
						if (edited.error === 'standard_switch_runtime_member_conflict') {
							showError(
								'ports',
								"Detach any unmanaged live bridge members before changing the switch's VLAN configuration"
							);
							return;
						}
						handleAPIError(edited);
						toast.error('Error updating switch', { position: 'bottom-center' });
						return;
					}

					toast.success(`Switch ${confirmModals.editSwitch.name} updated`, {
						position: 'bottom-center'
					});
				}

				reload = true;
				resetModal(true);
			} finally {
				saving = false;
			}
		}
	}

	let tableData = $derived(generateTableData(switches.current));
	let activeRows: SwitchRow[] | null = $state(null);
	let activeRow: SwitchRow | null = $derived(
		activeRows ? (activeRows[0] as SwitchRow) : ({} as SwitchRow)
	);

	function handleDelete() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			confirmModals.active = 'deleteSwitch';
			confirmModals.deleteSwitch.open = true;
			confirmModals.deleteSwitch.name = activeRow.name;
			confirmModals.deleteSwitch.id = activeRow.id as number;
		}
	}

	function deleteErrorMessage(error: APIResponse['error']): string {
		if (typeof error !== 'string') return 'Error deleting switch';
		switch (error) {
			case 'standard_switch_in_use_by_vm':
				return 'Switch is in use by a VM';
			case 'standard_switch_in_use_by_jail':
				return 'Switch is in use by a jail';
			case 'standard_switch_in_use_by_dhcp_config':
				return 'Switch is enabled in the DHCP configuration';
			case 'standard_switch_in_use_by_dhcp_range':
				return 'Switch is in use by a DHCP range';
			case 'standard_switch_in_use_by_static_route':
				return 'Switch is referenced by a static route';
			case 'standard_switch_in_use_by_firewall':
				return 'Switch is referenced by a firewall rule';
			case 'standard_switch_in_use_by_dynamic_dns':
				return 'Switch is used by Dynamic DNS';
			case 'standard_switch_in_use_by_mdns':
				return 'Switch is used by mDNS';
			case 'standard_switch_in_use_by_samba':
				return 'Switch is used by Samba';
			case 'standard_switch_in_use_by_wireguard':
				return 'Switch is used by WireGuard';
			case 'standard_switch_runtime_member_conflict':
				return 'Switch still contains an unmanaged interface';
			case 'standard_switch_not_found':
				return 'Switch no longer exists';
			default:
				return 'Error deleting switch';
		}
	}

	function handleEdit() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			activeTab = 'general';
			confirmModals.active = 'editSwitch';
			confirmModals.editSwitch.open = true;
			confirmModals.editSwitch.oldName = activeRow.name;
			confirmModals.editSwitch.name = activeRow.name;
			confirmModals.editSwitch.mtu = String(activeRow.mtu ?? '');
			confirmModals.editSwitch.vlan = activeRow.vlan === '-' ? '' : String(activeRow.vlan ?? '');

			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gw.value = '';
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gw.value = '';

			if (activeRow.networkObj && activeRow.networkObj.id) {
				comboBoxes.ipv4.value = activeRow.networkObj.id.toString();
			} else if (activeRow.networkManual) {
				comboBoxes.ipv4.value = activeRow.networkManual as string;
			}

			if (activeRow.network6Obj && activeRow.network6Obj.id) {
				comboBoxes.ipv6.value = activeRow.network6Obj.id.toString();
			} else if (activeRow.network6Manual) {
				comboBoxes.ipv6.value = activeRow.network6Manual as string;
			}

			if (activeRow.gatewayAddressObj && activeRow.gatewayAddressObj.id) {
				comboBoxes.ipv4Gw.value = activeRow.gatewayAddressObj.id.toString();
			} else if (activeRow.gatewayManual) {
				comboBoxes.ipv4Gw.value = activeRow.gatewayManual as string;
			}

			if (activeRow.gateway6AddressObj && activeRow.gateway6AddressObj.id) {
				comboBoxes.ipv6Gw.value = activeRow.gateway6AddressObj.id.toString();
			} else if (activeRow.gateway6Manual) {
				comboBoxes.ipv6Gw.value = activeRow.gateway6Manual as string;
			}

			confirmModals.editSwitch.disableIPv6 = (activeRow.disableIPv6 as boolean) || false;
			confirmModals.editSwitch.private = (activeRow.private as boolean) || false;
			confirmModals.editSwitch.dhcp = (activeRow.dhcp as boolean) || false;
			confirmModals.editSwitch.slaac = (activeRow.slaac as boolean) || false;
			confirmModals.editSwitch.defaultRoute = (activeRow.defaultRoute as boolean) || false;
			confirmModals.editSwitch.defaultRoute6 = (activeRow.defaultRoute6 as boolean) || false;
			confirmModals.editSwitch.disableBridgeOffloads =
				(activeRow.disableBridgeOffloads as boolean) || false;
			confirmModals.editSwitch.vlanFiltering = activeRow.vlanFiltering || false;
			confirmModals.editSwitch.defaultAccessVlan =
				activeRow.defaultAccessVlan === null ? '' : String(activeRow.defaultAccessVlan);
			confirmModals.editSwitch.hostVlan =
				activeRow.hostVlan === null ? '' : String(activeRow.hostVlan);
			confirmModals.editSwitch.portPolicies = Object.fromEntries(
				activeRow.ports.map((port) => [
					port.name,
					{
						mode: port.vlanPolicy.mode,
						untaggedVlan:
							port.vlanPolicy.untaggedVlan === undefined
								? ''
								: String(port.vlanPolicy.untaggedVlan),
						taggedVlans: port.vlanPolicy.taggedVlans.join(',')
					}
				])
			);

			comboBoxes.ports.value = activeRow.ports.map((port: { name: string }) => port.name);
			confirmModals.editSwitch.bridgeMacMode = activeRow.bridgeMacMode;
			comboBoxes.bridgeMacPort.value = activeRow.bridgeMacSourcePort || '';
			comboBoxes.bridgeMacObject.value = activeRow.bridgeMacObjectId?.toString() || '';
		}
	}

	function resetModal(close: boolean = true) {
		if (close) {
			confirmModals.newSwitch.open = false;
			confirmModals.deleteSwitch.open = false;
			confirmModals.editSwitch.open = false;
			addressRemovalWarning.open = false;
			rcConflictWarning.open = false;
			hostLayer3RemovalWarning.open = false;
		}
		addressRemovalWarning.ports = [];
		addressRemovalWarning.inspectionUnavailable = false;
		addressRemovalWarning.confirmations = emptyConfirmations();
		hostLayer3RemovalWarning.settings = [];
		hostLayer3RemovalWarning.confirmations = emptyConfirmations();
		rcConflictWarning.confirmations = emptyConfirmations();
		rcConflictWarning.conflicts = [];

		confirmModals.newSwitch.name = '';
		confirmModals.newSwitch.mtu = '';
		confirmModals.newSwitch.vlan = '';
		confirmModals.newSwitch.disableIPv6 = false;
		confirmModals.newSwitch.private = false;
		confirmModals.newSwitch.dhcp = false;
		confirmModals.newSwitch.slaac = false;
		confirmModals.newSwitch.defaultRoute = false;
		confirmModals.newSwitch.defaultRoute6 = false;
		confirmModals.newSwitch.disableBridgeOffloads = true;
		confirmModals.newSwitch.bridgeMacMode = '';
		confirmModals.newSwitch.vlanFiltering = false;
		confirmModals.newSwitch.defaultAccessVlan = '';
		confirmModals.newSwitch.hostVlan = '';
		confirmModals.newSwitch.portPolicies = {};

		confirmModals.editSwitch.name = '';
		confirmModals.editSwitch.mtu = '';
		confirmModals.editSwitch.vlan = '';
		confirmModals.editSwitch.disableIPv6 = false;
		confirmModals.editSwitch.private = false;
		confirmModals.editSwitch.dhcp = false;
		confirmModals.editSwitch.slaac = false;
		confirmModals.editSwitch.defaultRoute = false;
		confirmModals.editSwitch.defaultRoute6 = false;
		confirmModals.editSwitch.disableBridgeOffloads = false;
		confirmModals.editSwitch.bridgeMacMode = '';
		confirmModals.editSwitch.vlanFiltering = false;
		confirmModals.editSwitch.defaultAccessVlan = '';
		confirmModals.editSwitch.hostVlan = '';
		confirmModals.editSwitch.portPolicies = {};
		activeTab = 'general';

		comboBoxes.ipv4.value = '';
		comboBoxes.ipv4Gw.value = '';
		comboBoxes.ipv6.value = '';
		comboBoxes.ipv6Gw.value = '';
		comboBoxes.ports.value = [];
		comboBoxes.bridgeMacPort.value = '';
		comboBoxes.bridgeMacObject.value = '';

		if (close) {
			activeRows = null;
		}
	}

	watch(
		[() => confirmModals.newSwitch.slaac, () => confirmModals.editSwitch.slaac],
		([nwSLAAC, editSLAAC]) => {
			if (nwSLAAC) {
				confirmModals.newSwitch.disableIPv6 = false;
			}

			if (editSLAAC) {
				confirmModals.editSwitch.disableIPv6 = false;
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.disableIPv6, () => confirmModals.editSwitch.disableIPv6],
		([nwDisableIPv6, editDisableIPv6]) => {
			if (nwDisableIPv6) {
				confirmModals.newSwitch.slaac = false;
				confirmModals.newSwitch.defaultRoute6 = false;
			}

			if (editDisableIPv6) {
				confirmModals.editSwitch.slaac = false;
				confirmModals.editSwitch.defaultRoute6 = false;
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.dhcp, () => confirmModals.editSwitch.dhcp],
		([nwDHCP, editDHCP]) => {
			if (nwDHCP || editDHCP) {
				comboBoxes.ipv4.value = '';
				comboBoxes.ipv4Gw.value = '';
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.slaac, () => confirmModals.editSwitch.slaac],
		([nwSLAAC, editSLAAC]) => {
			if (nwSLAAC || editSLAAC) {
				comboBoxes.ipv6.value = '';
				comboBoxes.ipv6Gw.value = '';
			}
		}
	);

	watch(
		() => comboBoxes.ports.value,
		(ports) => {
			ensurePortPolicyDrafts(ports);
			if (!ports.includes(comboBoxes.bridgeMacPort.value)) {
				comboBoxes.bridgeMacPort.value = '';
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.vlanFiltering, () => confirmModals.editSwitch.vlanFiltering],
		([newFiltering, editFiltering]) => {
			if (newFiltering) {
				confirmModals.newSwitch.vlan = '';
			}
			if (editFiltering) {
				confirmModals.editSwitch.vlan = '';
			}
		}
	);

	watch(
		() => query,
		() => {
			activeRows = null;
		}
	);
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if type === 'edit'}
			<Button onclick={handleEdit} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{:else if type === 'delete'}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => {
				confirmModals.active = 'newSwitch';
				confirmModals.newSwitch.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		name="tt-switches"
		data={tableData}
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
	/>
</div>

{#if confirmModals.active === 'newSwitch'}
	<StandardSwitchForm
		mode="create"
		bind:form={confirmModals.newSwitch}
		bind:comboBoxes
		bind:activeTab
		portOptions={hostInterfacePortOptions(useablePorts)}
		{ipv4NetworkOptions}
		{ipv4GatewayOptions}
		{ipv6NetworkOptions}
		{ipv6GatewayOptions}
		{bridgeMACPortOptions}
		{bridgeMACObjectOptions}
		{effectiveBridgeMAC}
		{saving}
		onReset={() => resetModal(false)}
		onClose={() => resetModal(false)}
		onSubmit={() => confirmAction()}
		onCreateMACObject={openBridgeMACObjectCreator}
	/>
{:else if confirmModals.active === 'editSwitch'}
	<StandardSwitchForm
		mode="edit"
		bind:form={confirmModals.editSwitch}
		bind:comboBoxes
		bind:activeTab
		portOptions={hostInterfacePortOptions(useablePorts, activeRow?.portsOnly)}
		{ipv4NetworkOptions}
		{ipv4GatewayOptions}
		{ipv6NetworkOptions}
		{ipv6GatewayOptions}
		{bridgeMACPortOptions}
		{bridgeMACObjectOptions}
		{effectiveBridgeMAC}
		{saving}
		onReset={() => resetModal(false)}
		onClose={() => resetModal(false)}
		onSubmit={() => confirmAction()}
		onCreateMACObject={openBridgeMACObjectCreator}
	/>
{/if}
{#if bridgeMACObjectModal.open}
	<NetworkObjectCreator
		bind:open={bridgeMACObjectModal.open}
		edit={false}
		networkObjects={networkObjects.current}
		prefill={bridgeMACObjectModal.prefill}
		singleMAC={true}
		afterChange={async () => {
			await networkObjects.refetch();
		}}
		onCreated={(id) => {
			comboBoxes.bridgeMacObject.value = id.toString();
		}}
	/>
{/if}

<AlertDialog
	bind:open={addressRemovalWarning.open}
	customTitle={addressRemovalWarningMessage}
	confirmLabel="Continue"
	loadingLabel="Applying…"
	keepOpenOnConfirm={true}
	actions={{
		onConfirm: async () => {
			await confirmAction({
				...addressRemovalWarning.confirmations,
				addressRemoval: true
			});
			addressRemovalWarning.open = false;
		},
		onCancel: () => {
			addressRemovalWarning.open = false;
			addressRemovalWarning.ports = [];
			addressRemovalWarning.inspectionUnavailable = false;
			addressRemovalWarning.confirmations = emptyConfirmations();
		}
	}}
></AlertDialog>

<AlertDialog
	bind:open={hostLayer3RemovalWarning.open}
	customTitle={hostLayer3RemovalWarningMessage}
	confirmLabel="Remove host settings"
	loadingLabel="Applying…"
	keepOpenOnConfirm={true}
	actions={{
		onConfirm: async () => {
			await confirmAction({
				...hostLayer3RemovalWarning.confirmations,
				hostLayer3Removal: true
			});
			hostLayer3RemovalWarning.open = false;
		},
		onCancel: () => {
			hostLayer3RemovalWarning.open = false;
			hostLayer3RemovalWarning.settings = [];
			hostLayer3RemovalWarning.confirmations = emptyConfirmations();
		}
	}}
></AlertDialog>

<AlertDialog
	bind:open={rcConflictWarning.open}
	customTitle={rcConflictWarningMessage}
	confirmLabel="Continue"
	loadingLabel="Applying…"
	keepOpenOnConfirm={true}
	actions={{
		onConfirm: async () => {
			await confirmAction({
				...rcConflictWarning.confirmations,
				rcConflicts: true
			});
			rcConflictWarning.open = false;
		},
		onCancel: () => {
			rcConflictWarning.open = false;
			rcConflictWarning.conflicts = [];
			rcConflictWarning.confirmations = emptyConfirmations();
		}
	}}
></AlertDialog>

<AlertDialog
	open={confirmModals.deleteSwitch.open}
	keepOpenOnConfirm={true}
	names={{ parent: 'switch', element: confirmModals.deleteSwitch.name }}
	actions={{
		onConfirm: async () => {
			const result = await deleteSwitch(confirmModals.deleteSwitch.id);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(deleteErrorMessage(result.error), {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(`Switch ${confirmModals.deleteSwitch.name} deleted`, {
				position: 'bottom-center'
			});
			reload = true;
			resetModal(true);
		},
		onCancel: () => {
			resetModal(true);
		}
	}}
></AlertDialog>
