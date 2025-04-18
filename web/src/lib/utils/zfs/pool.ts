import type { APIResponse } from '$lib/types/common';
import type { Column, Row } from '$lib/types/components/tree-table';
import type { Zpool } from '$lib/types/zfs/pool';
import humanFormat, { type ScaleLike } from 'human-format';
import { getTranslation } from '../i18n';
import { generateNumberFromString } from '../numbers';

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
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name'
		},
		{
			field: 'size',
			title: 'Size'
		},
		{
			field: 'used',
			title: 'Used'
		},
		{
			field: 'health',
			title: 'Health'
		},
		{
			field: 'redundancy',
			title: 'Redundancy'
		}
	];

	for (const pool of pools) {
		const poolRow = {
			id: generateNumberFromString(pool.name),
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
					redundancy = 'RAIDZ ' + vdev.name.match(/raidz-?(\d+)/i)?.[1];
					vdevLabel = vdev.name.replace(/^raidz/i, 'RAIDZ');
				}

				const vdevRow = {
					id: generateNumberFromString(vdev.name),
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
						id: generateNumberFromString(device.name),
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
							id: generateNumberFromString(replacing.oldDrive.name),
							name: `${replacing.oldDrive.name} [OLD]`,
							size: humanFormat(replacing.oldDrive.size, sizeOptions),
							used: '-',
							health: `${replacing.oldDrive.health} (Being replaced)`,
							redundancy: '-',
							children: []
						});

						vdevRow.children.push({
							id: generateNumberFromString(replacing.newDrive.name),
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
					id: generateNumberFromString(vdev.devices[0].name),
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

export function isPool(pools: Zpool[], name: string): boolean {
	return pools.some((pool) => pool.name === name);
}

export function isReplaceableDevice(pools: Zpool[], name: string): boolean {
	for (const pool of pools) {
		if (pool.vdevs.some((vdev) => vdev.name === name)) {
			return false; // False if we're striped
		}
	}

	return pools.some((pool) => {
		for (const vdev of pool.vdevs) {
			if (vdev.devices.some((device) => device.name === name)) {
				return true;
			}
		}
		return false;
	});
}

export function getPoolByDevice(pools: Zpool[], name: string): string {
	for (const pool of pools) {
		for (const vdev of pool.vdevs) {
			if (vdev.devices.some((device) => device.name === name)) {
				return pool.name;
			}
		}
	}

	return '';
}

export function parsePoolActionError(error: APIResponse): string {
	if (error.message && error.message === 'pool_create_failed') {
		if (error.error) {
			if (error.error.includes('mirror contains devices of different sizes')) {
				return getTranslation(
					'zfs.pool.errors.pool_create_failed_mirror_different_sizes',
					'Pool contains a mirror with devices of different sizes'
				);
			} else if (error.error.includes('raidz contains devices of different sizes')) {
				return getTranslation(
					'zfs.pool.errors.pool_create_failed_raidz_different_sizes',
					'Pool contains a raidz vdev with devices of different sizes'
				);
			}
		}
	}

	if (error.message && error.message === 'pool_delete_failed') {
		if (error.error) {
			if (error.error.includes('pool or dataset is busy')) {
				return getTranslation('zfs.pool.errors.pool_delete_failed_busy', 'Pool is busy');
			}
		}
	}

	return '';
}
