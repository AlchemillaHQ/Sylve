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
				if (row.type === 'iso') {
					return renderWithIcon('tdesign:cd-filled', value, 'text-green-500', 'CD-ROM');
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
                switch (value) {
                    case 'ahci-cd':
                        return 'AHCI-CD';
                    case 'virtio-blk':
                        return 'VirtIO-BLK';
                    case 'ahci-hd':
                        return 'AHCI-HD';
                    default:
                        break;
                }
                return '-';
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

	for (const storage of storages) {
		let name = '';
		let size = 0;

		if (storage.detached) {
			continue;
		}

		if (storage.type === 'iso') {
			const download = downloads.find((d) => d.uuid === storage.dataset);
			name = download ? download.name : 'Unknown ISO';
			size = download ? download.size : 0;
		} else if (storage.type === 'zvol' || storage.type === 'raw') {
			for (const dataset of datasets) {
				let found: Dataset | null = null;

				if (dataset.guid === storage.dataset) {
					found = dataset;
				}

				if (found) {
					name = (found as Dataset).name;
					if (storage.type === 'raw') {
						if (storage.name?.endsWith('.img')) {
							name += `/${storage.name}`;
						} else {
							name += `/${storage.name}.img`;
						}
						size = storage.size || 0;
					} else if (storage.type === 'zvol') {
						if (dataset.volsize) {
							size = dataset.volsize;
						}
					}

					break;
				}
			}
		}

		rows.push({
			id: storage.id,
			type: storage.type,
			emulation: storage.emulation,
			name: name,
			size: size
		});
	}

	return {
		rows: rows,
		columns
	};
}
