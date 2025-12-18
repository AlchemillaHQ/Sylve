import type { APIResponse } from '$lib/types/common';
import type { Column, Row } from '$lib/types/components/tree-table';
import type { GroupedByPool } from '$lib/types/zfs/dataset';
import { generateNumberFromString } from '$lib/utils/numbers';
import { renderWithIcon, sizeFormatter } from '$lib/utils/table';
import { cleanChildren } from '$lib/utils/tree-table';
import { toast } from 'svelte-sonner';

export const createFSProps = {
	atime: [
		{
			label: 'On',
			value: 'on'
		},
		{
			label: 'Off',
			value: 'off'
		}
	],
	checksum: [
		{
			label: 'On',
			value: 'on'
		},
		{
			label: 'Off',
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
			label: 'On',
			value: 'on'
		},
		{
			label: 'Off',
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
			label: 'Off',
			value: 'off'
		},
		{
			label: 'On',
			value: 'on'
		},
		{
			label: 'Verify',
			value: 'verify'
		}
	],
	encryption: [
		{
			label: 'Off',
			value: 'off'
		},
		{
			label: 'On',
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
	aclInherit: [
		{
			label: 'Discard',
			value: 'discard'
		},
		{
			label: 'No Allow',
			value: 'noallow'
		},
		{
			label: 'Restricted',
			value: 'restricted'
		},
		{
			label: 'Passthrough',
			value: 'passthrough'
		},
		{
			label: 'Passthrough-X',
			value: 'passthrough-x'
		}
	],
	aclMode: [
		{
			label: 'Discard',
			value: 'discard'
		},
		{
			label: 'Group Mask',
			value: 'groupmask'
		},
		{
			label: 'Passthrough',
			value: 'passthrough'
		},
		{
			label: 'Passthrough-X',
			value: 'passthrough-x'
		},
		{
			label: 'Restricted',
			value: 'restricted'
		}
	],
	recordsize: [
		{
			label: '8K - Postgres',
			value: '8192'
		},
		{
			label: '16K - MySQL',
			value: '16384'
		},
		{
			label: '128K - default',
			value: '131072'
		},
		{
			label: '1M - Large Files',
			value: '1048576'
		}
	]
};

export function generateTableData(grouped: GroupedByPool[]): { rows: Row[]; columns: Column[] } {
	const columns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'name',
			title: 'Name',
			formatter: (cell) => {
				const value = cell.getValue();
				if (value.includes('/')) {
					return renderWithIcon('material-symbols:files', value.substring(value.indexOf('/') + 1));
				}
				return renderWithIcon('bi:hdd-stack-fill', value);
			}
		},
		{ field: 'used', title: 'Used', formatter: sizeFormatter },
		{ field: 'available', title: 'Available', formatter: sizeFormatter },
		{ field: 'referenced', title: 'Referenced', formatter: sizeFormatter },
		{ field: 'mountpoint', title: 'Mount Point' },
		{ field: 'type', title: 'Type', visible: false }
	];

	const rows: Row[] = [];

	for (const group of grouped) {
		const nodeMap = new Map<string, Row>();

		/** helper */
		const getOrCreate = (name: string, type = 'FILESYSTEM'): Row => {
			let node = nodeMap.get(name);
			if (!node) {
				node = {
					id: generateNumberFromString(name),
					name,
					used: 0,
					available: 0,
					referenced: 0,
					mountpoint: '',
					children: [],
					type
				};
				nodeMap.set(name, node);
			}
			return node;
		};

		// pool root
		const poolNode = getOrCreate(group.name, 'pool');

		// ---------- PASS 1: create all nodes ----------
		for (const fs of group.filesystems) {
			const parts = fs.name.split('/');
			for (let i = 0; i < parts.length; i++) {
				const path = parts.slice(0, i + 1).join('/');
				getOrCreate(path);
			}
		}

		// ---------- PASS 2: apply stats + build tree ----------
		for (const fs of group.filesystems) {
			const node = nodeMap.get(fs.name)!;

			node.used = fs.used;
			node.available = fs.available;
			node.referenced = fs.referenced;
			node.mountpoint = fs.mountpoint || '';
			node.type = fs.type;

			if (fs.name !== group.name) {
				const parentName = fs.name.substring(0, fs.name.lastIndexOf('/'));
				const parent = nodeMap.get(parentName);
				parent?.children!.push(node);
			}
		}

		rows.push(cleanChildren(poolNode));
	}

	return { rows, columns };
}

export function handleError(error: APIResponse): void {
	if (error.error?.includes('dataset already exists')) {
		let value = '';

		if (error.error?.includes('snapshot')) {
			value = 'Snapshot already exists';
		} else {
			value = 'Filesystem already exists';
		}

		toast.error(value, {
			position: 'bottom-center'
		});
	}

	if (error.error?.includes('numeric value is too large')) {
		toast.error('Numeric value is too large', {
			position: 'bottom-center'
		});
	}

	if (error.error?.includes('invalid_encryption_key_length')) {
		toast.error('Invalid encryption key length', {
			position: 'bottom-center'
		});
	}

	if (error.error?.includes('pool or dataset is busy')) {
		toast.error('Pool or dataset is busy', {
			position: 'bottom-center'
		});
	}
}
