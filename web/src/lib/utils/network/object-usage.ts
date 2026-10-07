import type {
	NetworkObject,
	NetworkObjectRow,
	NetworkObjectUsage
} from '$lib/types/network/object';
import type { CellComponent } from 'tabulator-tables';

export const networkObjectUsageTypes: Record<
	NetworkObjectUsage['type'],
	{ label: string; color: string; icon: string }
> = {
	vm: {
		label: 'VM',
		color: 'text-blue-400 border-blue-400/50',
		icon: 'icon-[mdi--desktop-classic]'
	},
	jail: {
		label: 'Jail',
		color: 'text-violet-400 border-violet-400/50',
		icon: 'icon-[mdi--cube-outline]'
	},
	switch: {
		label: 'Switch',
		color: 'text-cyan-400 border-cyan-400/50',
		icon: 'icon-[mdi--switch]'
	},
	dhcp: {
		label: 'DHCP lease',
		color: 'text-amber-400 border-amber-400/50',
		icon: 'icon-[mdi--ip-network-outline]'
	},
	'firewall-traffic': {
		label: 'Traffic rule',
		color: 'text-emerald-400 border-emerald-400/50',
		icon: 'icon-[mdi--shield-outline]'
	},
	'firewall-nat': {
		label: 'NAT rule',
		color: 'text-orange-400 border-orange-400/50',
		icon: 'icon-[mdi--swap-horizontal]'
	},
	route: {
		label: 'Static route',
		color: 'text-pink-400 border-pink-400/50',
		icon: 'icon-[mdi--routes]'
	}
};

export function getNetworkObjectUsageLabels(
	object: Pick<NetworkObject, 'usedBy' | 'isUsed' | 'isUsedBy'>
): string[] {
	const labels = (object.usedBy ?? []).map((usage) => {
		const type = networkObjectUsageTypes[usage.type].label;
		return usage.name ? `${type}: ${usage.name}` : type;
	});
	if (labels.length > 0 || !object.isUsed) return labels;

	const legacyTypes: Record<string, string> = {
		dhcp: 'DHCP',
		firewall: 'Firewall',
		switch: 'Switch',
		route: 'Static route'
	};
	return [legacyTypes[object.isUsedBy] ?? (object.isUsedBy || 'In use')];
}

export function formatNetworkObjectUsage(cell: CellComponent): HTMLElement {
	const { usageLabels, usages } = cell.getRow().getData() as NetworkObjectRow;
	const container = document.createElement('div');
	container.className = 'flex items-center gap-1 overflow-hidden';
	container.title = usageLabels.join('\n');

	if (usageLabels.length === 0) {
		container.className = 'text-muted-foreground';
		container.textContent = '-';
		return container;
	}

	for (const [index, label] of usageLabels.slice(0, 2).entries()) {
		const usage = usages[index];
		const details = usage ? networkObjectUsageTypes[usage.type] : null;
		const color = details?.color ?? 'text-muted-foreground border-muted-foreground/50';
		const badge = document.createElement('span');
		badge.className = `inline-flex min-w-0 max-w-40 items-center gap-1 rounded border px-1 font-mono text-xs leading-tight ${color}`;

		const icon = document.createElement('span');
		icon.className = `h-3 w-3 shrink-0 ${details?.icon ?? 'icon-[mdi--link-variant]'}`;
		icon.setAttribute('aria-hidden', 'true');
		badge.appendChild(icon);

		const name = document.createElement('span');
		name.className = 'truncate';
		name.textContent = usage?.name || label;
		badge.appendChild(name);
		container.appendChild(badge);
	}
	if (usageLabels.length > 2) {
		const count = document.createElement('span');
		count.className = 'shrink-0 text-xs text-muted-foreground';
		count.textContent = `+${usageLabels.length - 2}`;
		container.appendChild(count);
	}

	return container;
}
