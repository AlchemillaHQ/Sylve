import type { Iface } from '$lib/types/network/iface';
import type { SwitchList } from '$lib/types/network/switch';

export interface HostInterfaceOption {
	value: string;
	label: string;
	disabled?: boolean;
	description?: string;
}

interface BuildHostInterfaceOptionsInput {
	interfaces: readonly Iface[];
	switches?: SwitchList;
	includeInterface?: (iface: Iface) => boolean;
	getInterfaceLabel?: (iface: Iface) => string;
}

function defaultInterfaceLabel(iface: Iface): string {
	const description = iface.description?.trim();
	return description ? `${description} (${iface.name})` : iface.name;
}

export function buildHostInterfaceOptions({
	interfaces,
	switches = { standard: [], manual: [] },
	includeInterface = () => true,
	getInterfaceLabel = defaultInterfaceLabel
}: BuildHostInterfaceOptionsInput): HostInterfaceOption[] {
	const eligibleInterfaces = interfaces.filter((iface) => iface.name && includeInterface(iface));
	const interfacesByName = new Map(eligibleInterfaces.map((iface) => [iface.name, iface]));
	const covered = new Set<string>();
	const options: HostInterfaceOption[] = [];

	const add = (option: HostInterfaceOption) => {
		if (!option.value || covered.has(option.value)) return;
		covered.add(option.value);
		options.push(option);
	};

	for (const networkSwitch of switches.standard ?? []) {
		if (networkSwitch.vlanFiltering) {
			add({
				value: networkSwitch.bridgeName,
				label: `${networkSwitch.name} (${networkSwitch.bridgeName}) — L2 only`,
				disabled: true,
				description: 'Select the Host VLAN interface for host networking'
			});

			if (networkSwitch.hostVlan !== null) {
				const hostInterfaceName = `${networkSwitch.bridgeName}.${networkSwitch.hostVlan}`;
				if (interfacesByName.has(hostInterfaceName)) {
					add({
						value: hostInterfaceName,
						label: `${networkSwitch.name} · Host VLAN ${networkSwitch.hostVlan} (${hostInterfaceName})`,
						description: 'Host-facing interface for this VLAN-filtered switch'
					});
				}
			}
			continue;
		}

		add({
			value: networkSwitch.bridgeName,
			label: `${networkSwitch.name} (${networkSwitch.bridgeName})`
		});
	}

	for (const networkSwitch of switches.manual ?? []) {
		add({ value: networkSwitch.bridge, label: `${networkSwitch.name} (${networkSwitch.bridge})` });
	}

	for (const iface of eligibleInterfaces) {
		add({ value: iface.name, label: getInterfaceLabel(iface) });
	}

	return options;
}

export function getFriendlyName(i: string, switches: SwitchList, interfaces: Iface[]): string {
	if (!i) return i;
	const stdSwitch = switches.standard?.find((sw) => sw.bridgeName === i);
	if (stdSwitch) return stdSwitch.name;
	const manSwitch = switches.manual?.find((sw) => sw.bridge === i);
	if (manSwitch) return manSwitch.name;
	const iface = interfaces.find((iface) => iface.name === i);
	if (iface?.description) return iface.description;
	return i;
}
