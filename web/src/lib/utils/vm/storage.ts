import type { Column, Row } from '$lib/types/components/tree-table';
import type { Download } from '$lib/types/utilities/downloader';
import type { VM } from '$lib/types/vm/vm';
import type { Dataset } from '$lib/types/zfs/dataset';
import type { CellComponent } from 'tabulator-tables';
import { formatBytesBinary } from '../bytes';
import { escapeHTML } from '../string';
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
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				return cell.getValue() === true
					? renderWithIcon('mdi:check-circle', 'Connected', 'text-green-500')
					: renderWithIcon('mdi:circle-outline', 'Disconnected', 'text-muted-foreground');
			}
		},
		{
			field: 'type',
			title: 'Backing',
			formatter: (cell: CellComponent) => {
				switch (cell.getValue()) {
					case 'zvol':
						return renderWithIcon('carbon:volume-block-storage', 'ZVOL', 'text-muted-foreground');
					case 'raw':
						return renderWithIcon('carbon:document', 'RAW', 'text-muted-foreground');
					case 'image':
						return renderWithIcon('tdesign:cd-filled', 'Media', 'text-muted-foreground');
					case 'filesystem':
						return renderWithIcon('mdi:folder-network', '9P', 'text-muted-foreground');
					default:
						return '-';
				}
			}
		},
		{
			field: 'access',
			title: 'Access',
			formatter: (cell: CellComponent) =>
				cell.getValue() === 'ro'
					? renderWithIcon('lucide:lock-keyhole', 'RO', 'text-muted-foreground', 'Read-only')
					: renderWithIcon('lucide:lock-keyhole-open', 'RW', 'text-muted-foreground', 'Writable')
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => escapeHTML(String(cell.getValue() ?? ''))
		},
		{
			field: 'emulation',
			title: 'Emulation',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				switch (value) {
					case 'ahci-cd':
						return 'AHCI CD-ROM';
					case 'virtio-blk':
						return 'VirtIO Block';
					case 'ahci-hd':
						return 'AHCI Hard Disk';
					case 'nvme':
						return 'NVMe';
					case 'virtio-9p':
						return 'VirtIO 9P';
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

				return formatBytesBinary(value);
			}
		},
		{
			field: 'path',
			title: 'Path',
			copyOnClick: true,
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (!value) return '-';
				return escapeHTML(String(value));
			}
		}
	];

	const storages = vm.storages || [];

	let zvolCount = 0;
	let rawCount = 0;

	for (const storage of storages) {
		let name = '';
		let size = 0;

		if (storage.type === 'image') {
			const download = downloads.find((d) => storage.uuid === d.uuid);
			const downloadName = download ? download.name : 'Unknown ISO';
			size = download ? download.size : 0;

			name = storage.name?.trim() ? `${storage.name.trim()} (${downloadName})` : downloadName;
		} else if (storage.type === 'zvol' || storage.type === 'raw') {
			if (storage.type === 'zvol') {
				zvolCount++;
				name = storage.name ? storage.name : `ZFS Volume - ${zvolCount}`;
			} else if (storage.type === 'raw') {
				rawCount++;
				name = storage.name ? storage.name : `Raw Disk - ${rawCount}`;
			}
		} else if (storage.type === 'filesystem') {
			const datasetName = storage.dataset?.name || 'Unknown dataset';
			const target = storage.filesystemTarget || storage.name || `share-${storage.id}`;
			name = `${target} (${datasetName})`;
			size = 0;
		}

		let path = '';

		if (storage.type === 'image') {
			const download = downloads.find((d) => storage.uuid === d.uuid);
			path = download?.extractedPath || download?.path || '';
		} else if (storage.type === 'raw') {
			const datasetName =
				storage.dataset?.name ||
				`${storage.pool}/sylve/virtual-machines/${vm.rid}/raw-${storage.id}`;
			const dataset = datasets.find((item) => item.name === datasetName);
			const datasetStorageID = datasetName.match(/\/raw-(\d+)$/)?.[1] || storage.id;
			path = dataset?.mountpoint
				? `${dataset.mountpoint.replace(/\/$/, '')}/${datasetStorageID}.img`
				: datasetName;
		} else if (storage.type === 'zvol') {
			const datasetName =
				storage.dataset?.name ||
				`${storage.pool}/sylve/virtual-machines/${vm.rid}/zvol-${storage.id}`;
			path = `/dev/zvol/${datasetName}`;
		} else if (storage.type === 'filesystem') {
			const dataset = datasets.find((d) => d.name === storage.dataset?.name);
			path = dataset?.mountpoint || storage.dataset?.name || '';
		}

		rows.push({
			id: storage.id,
			enabled: storage.enable,
			type: storage.type,
			access: storage.type === 'image' || storage.readOnly ? 'ro' : 'rw',
			emulation: storage.emulation,
			bootorder: storage.type === 'filesystem' ? undefined : (storage.bootOrder ?? 0),
			name: name,
			size: size || storage.size,
			path: path || '-'
		});
	}

	return {
		rows: rows,
		columns
	};
}
