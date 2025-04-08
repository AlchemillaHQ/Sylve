import type { Column, Row } from '$lib/types/components/tree-table';
import type { Zpool } from '$lib/types/zfs/pool';
import humanFormat, { type ScaleLike } from 'human-format';

const sizeOptions = {
	scale: 'binary' as ScaleLike,
	unit: 'B',
	maxDecimals: 1
};

export function generateTableData(pools: Zpool[]): {
	rows: Row[];
	columns: Column[];
} {
	let rows: Row[] = [];
	let columns: Column[] = [
		{
			key: 'name',
			label: 'Name'
		},
		{
			key: 'size',
			label: 'Size'
		},
		{
			key: 'used',
			label: 'Used'
		},
		{
			key: 'health',
			label: 'Health'
		},
		{
			key: 'redundancy',
			label: 'Redundancy'
		}
	];

	let id = 0;

	for (const pool of pools) {
		const poolRow = {
			id: id++,
			name: pool.name,
			size: humanFormat(pool.size, sizeOptions),
			used: humanFormat(pool.allocated, sizeOptions),
			health: pool.health,
			redundancy: '',
			children: [] as Row[]
		};

		for (const vdev of pool.vdevs) {
			if (vdev.name.includes('mirror') || vdev.name.includes('raid') || vdev.devices.length > 1) {
				let redundancy = 'Stripe';
				let vdevLabel = vdev.name;

				if (vdev.name.startsWith('mirror')) {
					redundancy = 'Mirror';
					vdevLabel = vdev.name.replace(/mirror-?(\d+)/i, 'Mirror $1');
				} else if (vdev.name.startsWith('raidz')) {
					redundancy = 'RAIDZ';
					vdevLabel = vdev.name.replace(/^raidz/i, 'RAIDZ');
				}

				const vdevRow = {
					id: id++,
					name: vdevLabel,
					size: humanFormat(vdev.alloc + vdev.free, sizeOptions),
					used: humanFormat(vdev.alloc, sizeOptions),
					health: vdev.health,
					redundancy: '-',
					children: [] as Row[]
				};

				for (const device of vdev.devices) {
					if (
						vdev.replacingDevices &&
						vdev.replacingDevices.some(
							(r) => r.oldDrive.name === device.name || r.newDrive.name === device.name
						)
					) {
						continue;
					}

					vdevRow.children.push({
						id: id++,
						name: device.name,
						size: humanFormat(device.size, sizeOptions),
						used: '-',
						health: device.health,
						redundancy: '-',
						children: []
					});
				}

				if (vdev.replacingDevices && vdev.replacingDevices.length > 0) {
					for (const replacing of vdev.replacingDevices) {
						vdevRow.children.push({
							id: id++,
							name: `${replacing.oldDrive.name} [OLD]`,
							size: humanFormat(replacing.oldDrive.size, sizeOptions),
							used: '-',
							health: `${replacing.oldDrive.health} (Being replaced)`,
							redundancy: '-',
							children: []
						});

						vdevRow.children.push({
							id: id++,
							name: `${replacing.newDrive.name} [NEW]`,
							size: humanFormat(replacing.newDrive.size, sizeOptions),
							used: '-',
							health: `${replacing.newDrive.health} (Replacement)`,
							redundancy: '-',
							children: []
						});
					}
				}

				poolRow.children.push(vdevRow);
				poolRow.redundancy = redundancy;
			} else {
				poolRow.children.push({
					id: id++,
					name: vdev.devices[0].name,
					size: humanFormat(vdev.devices[0].size, sizeOptions),
					used: '-',
					health: vdev.devices[0].health,
					redundancy: '-',
					children: []
				});
				poolRow.redundancy = 'Stripe';
			}
		}

		rows.push(poolRow);
	}

	return {
		rows,
		columns
	};
}
