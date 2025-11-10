import type { Column, Row } from '$lib/types/components/tree-table';
import type { Download } from '$lib/types/utilities/downloader';
import type { VM } from '$lib/types/vm/vm';
import type { Dataset } from '$lib/types/zfs/dataset';
import humanFormat from 'human-format';
import type { CellComponent } from 'tabulator-tables';
import { renderWithIcon } from '../table';

export function generateTableData(
	vm: VM,
	datasets: Dataset[],
	downloads: Download[]
): {
	rows: Row[];
	columns: Column[];
} {
	const rows: Row[] = [];
	const columns: Column[] = [
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'type',
			title: 'Type',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const row = cell.getRow().getData();

				if (row.type === 'installation-media') {
					return renderWithIcon('tdesign:cd-filled', value, 'text-green-500', 'Installation Media');
				} else if (row.type === 'zvol') {
					return renderWithIcon(
						'carbon:volume-block-storage',
						value,
						'text-blue-500',
						'ZFS Volume'
					);
				} else if (row.type === 'raw') {
					return renderWithIcon('carbon:volume-block-storage', value, 'text-blue-500', 'Raw Disk');
				}
				return value;
			}
		},
		{
			field: 'emulation',
			title: 'Emulation',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				console.log('Emulation value:', value);
				switch (value) {
					case 'ahci-cd':
						return 'AHCI-CD';
					case 'virtio-blk':
						return 'VirtIO-BLK';
					case 'ahci-hd':
						return 'AHCI-HD';
					case 'nvme':
						return 'NVMe';
					default:
						break;
				}
				return '-';
			}
		},
		{
			field: 'bootorder',
			title: 'Boot Order',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value !== undefined ? value : '-';
			}
		},
		{
			field: 'size',
			title: 'Size',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value === 0) {
					return '-';
				}
				return humanFormat(value);
			}
		}
	];

	const storages = vm.storages || [];

	let zvolCount = 0;
	let rawCount = 0;

	for (const storage of storages) {
		let name = '';
		let size = 0;

		if (storage.type === 'installation-media') {
			const download = downloads.find((d) => storage.uuid === d.uuid);
			name = download ? download.name : 'Unknown ISO';
			size = download ? download.size : 0;
		} else if (storage.type === 'zvol' || storage.type === 'raw') {
			if (storage.type === 'zvol') {
				zvolCount++;
				name = `ZFS Volume - ${zvolCount}`;
			} else if (storage.type === 'raw') {
				rawCount++;
				name = `Raw Disk - ${rawCount}`;
			}
		}

		rows.push({
			id: storage.id,
			type: storage.type,
			emulation: storage.emulation,
			bootorder: storage.bootOrder || 0,
			name: name,
			size: size
		});
	}

	return {
		rows: rows,
		columns
	};
}
