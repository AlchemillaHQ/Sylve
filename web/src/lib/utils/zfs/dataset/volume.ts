import type { APIResponse } from '$lib/types/common';
import type { Column, Row } from '$lib/types/components/tree-table';
import type { GroupedByPool } from '$lib/types/zfs/dataset';
import { generateNumberFromString } from '$lib/utils/numbers';
import { renderWithIcon, sizeFormatter } from '$lib/utils/table';
import { cleanChildren } from '$lib/utils/tree-table';
import humanFormat from 'human-format';
import { toast } from 'svelte-sonner';

export const createVolProps = {
	atime: [
		{
			label: 'Yes',
			value: 'on'
		},
		{
			label: 'No',
			value: 'off'
		}
	],
	checksum: [
		{
			label: 'Yes',
			value: 'on'
		},
		{
			label: 'No',
			value: 'off'
		},
		{
			label: 'fletcher2',
			value: 'fletcher2'
		},
		{
			label: 'fletcher4',
			value: 'fletcher4'
		},
		{
			label: 'sha256',
			value: 'sha256'
		},
		{
			label: 'noparity',
			value: 'noparity'
		}
	],
	compression: [
		{
			label: 'Yes',
			value: 'on'
		},
		{
			label: 'No',
			value: 'off'
		},
		{
			label: 'gzip',
			value: 'gzip'
		},
		{
			label: 'lz4',
			value: 'lz4'
		},
		{
			label: 'lzjb',
			value: 'lzjb'
		},
		{
			label: 'zle',
			value: 'zle'
		},
		{
			label: 'zstd',
			value: 'zstd'
		},
		{
			label: 'zstd-fast',
			value: 'zstd-fast'
		}
	],
	dedup: [
		{
			label: 'No',
			value: 'off'
		},
		{
			label: 'Yes',
			value: 'on'
		},
		{
			label: 'Verify',
			value: 'verify'
		}
	],
	encryption: [
		{
			label: 'No',
			value: 'off'
		},
		{
			label: 'Yes',
			value: 'on'
		},
		{
			label: 'aes-128-ccm',
			value: 'aes-128-ccm'
		},
		{
			label: 'aes-192-ccm',
			value: 'aes-192-ccm'
		},
		{
			label: 'aes-256-ccm',
			value: 'aes-256-ccm'
		},
		{
			label: 'aes-128-gcm',
			value: 'aes-128-gcm'
		},
		{
			label: 'aes-192-gcm',
			value: 'aes-192-gcm'
		},
		{
			label: 'aes-256-gcm',
			value: 'aes-256-gcm'
		}
	],
	volblocksize: [
		{
			label: '512B (Legacy HDD sectors)',
			value: '512'
		},
		{
			label: '1K (1024B)',
			value: '1024'
		},
		{
			label: '2K (2048B)',
			value: '2048'
		},
		{
			label: '4K (4096B) - SSD/VMs',
			value: '4096'
		},
		{
			label: '8K (8192B)',
			value: '8192'
		},
		{
			label: '16K (16384B)',
			value: '16384'
		},
		{
			label: '32K (32768B) - Sequential workloads',
			value: '32768'
		},
		{
			label: '64K (65536B) - Large files',
			value: '65536'
		},
		{
			label: '128K (131072B) Media/backups',
			value: '131072'
		}
	],
	primarycache: [
		{
			label: 'All',
			value: 'all'
		},
		{
			label: 'Metadata',
			value: 'metadata'
		},
		{
			label: 'None',
			value: 'none'
		}
	],
	volmode: [
		{
			label: 'default',
			value: 'default'
		},
		{
			label: 'full',
			value: 'full'
		},
		{
			label: 'geom',
			value: 'geom'
		},
		{
			label: 'dev',
			value: 'dev'
		},
		{
			label: 'none',
			value: 'none'
		}
	]
};

export function generateTableData(grouped: GroupedByPool[]): { rows: Row[]; columns: Column[] } {
	const rows: Row[] = [];
	const columns: Column[] = [
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell) => {
				const value = cell.getValue();

				if (value.includes('/')) {
					const volume = value.substring(value.indexOf('/') + 1);
					return renderWithIcon('carbon:volume-block-storage', volume);
				}

				return renderWithIcon('bi:hdd-stack-fill', value);
			}
		},
		{
			field: 'size',
			title: 'Size',
			formatter: sizeFormatter
		},
		{
			field: 'used',
			title: 'Used',
			formatter: sizeFormatter
		},
		{
			field: 'referenced',
			title: 'Referenced',
			formatter: sizeFormatter
		},
		{
			field: 'guid',
			title: 'GUID',
			visible: false
		}
	];

	for (const group of grouped) {
		const poolRow: Row = {
			id: generateNumberFromString(group.name),
			name: group.name,
			size: '-',
			used: '-',
			referenced: '-',
			guid: undefined,
			children: [],
			type: 'pool'
		};

		const volumeChildren = group.volumes
			.filter((vol) => vol.name !== group.name)
			.map((vol) => ({
				id: generateNumberFromString(vol.name),
				name: vol.name,
				size: vol.properties?.volsize ?? '0',
				used: vol.used ?? '0',
				referenced: vol.referenced,
				guid: vol.guid,
				children: [],
				type: 'volume'
			}));

		poolRow.children?.push(...volumeChildren);
		rows.push(poolRow);
	}

	return {
		rows: rows.filter((row) => row.children && row.children.length > 0).map(cleanChildren),
		columns
	};
}

export function handleError(error: APIResponse): void {
	if (error.error?.includes('dataset_in_use_by_vm')) {
		toast.error('Dataset is in use by a VM', {
			position: 'bottom-center'
		});

		return;
	}

	if (error.error?.includes('dataset already exists')) {
		toast.error('Dataset already exists', {
			position: 'bottom-center'
		});
	}
}
