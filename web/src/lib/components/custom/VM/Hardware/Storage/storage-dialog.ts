import type { VMStorageEmulationType } from '$lib/types/vm/vm';

export type StorageIntent = 'empty' | 'from-image' | 'media' | 'filesystem' | 'advanced';
export type StorageDiskType = 'raw' | 'zvol' | 'image' | 'filesystem';
export type StorageEmulation = VMStorageEmulationType;
export type WritableStorageEmulation = Exclude<StorageEmulation, 'ahci-cd' | 'virtio-9p'>;

export interface AddStorageProperties {
	name: string;
	diskType: StorageDiskType;
	rawPath: string;
	size: string;
	filesystemTarget: string;
	filesystemReadOnly: boolean;
	emulation: StorageEmulation;
	pool: string;
	bootOrder: number | null;
	loading: boolean;
}

export interface OperationPreviewData {
	source: string;
	action: string;
	result: string;
	note: string;
	warning: boolean;
}

export const intentDetails = {
	empty: {
		title: 'Create empty disk',
		description: 'Allocate fresh writable storage managed by Sylve.',
		icon: 'icon-[carbon--data-volume]',
		action: 'Create disk',
		loading: 'Creating disk...'
	},
	'from-image': {
		title: 'Create disk from image',
		description: 'Copy a downloaded appliance image into a writable disk.',
		icon: 'icon-[lucide--copy-plus]',
		action: 'Create writable disk',
		loading: 'Copying image...'
	},
	media: {
		title: 'Attach read-only media',
		description: 'Reference an installer or image without copying it.',
		icon: 'icon-[tdesign--cd-filled]',
		action: 'Attach media',
		loading: 'Attaching media...'
	},
	filesystem: {
		title: 'Share filesystem',
		description: 'Expose an existing ZFS filesystem through VirtIO 9P.',
		icon: 'icon-[mdi--folder-network-outline]',
		action: 'Share filesystem',
		loading: 'Sharing filesystem...'
	},
	advanced: {
		title: 'Advanced host import',
		description: 'Host RAW paths and existing ZFS volumes.',
		icon: 'icon-[lucide--hard-drive-upload]',
		action: 'Import storage',
		loading: 'Importing storage...'
	}
} satisfies Record<
	StorageIntent,
	{
		title: string;
		description: string;
		icon: string;
		action: string;
		loading: string;
	}
>;

export const primaryIntents: StorageIntent[] = ['empty', 'from-image', 'media', 'filesystem'];

export const backingOptions = [
	{ value: 'zvol', label: 'ZFS Volume' },
	{ value: 'raw', label: 'RAW file' }
];

export const writableEmulationOptions = [
	{ value: 'nvme', label: 'NVMe' },
	{ value: 'virtio-blk', label: 'VirtIO Block' },
	{ value: 'ahci-hd', label: 'AHCI Hard Disk' }
];

export const mediaEmulationOptions = [
	{ value: 'ahci-cd', label: 'AHCI CD-ROM' },
	{ value: 'nvme', label: 'NVMe' },
	{ value: 'virtio-blk', label: 'VirtIO Block' },
	{ value: 'ahci-hd', label: 'AHCI Hard Disk' }
];

export const storageSelectClasses = {
	parent: 'min-w-0 space-y-1',
	label: 'flex h-7 items-center whitespace-nowrap text-sm',
	trigger: 'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
};

export function createAttachOptions(defaultPool: string): AddStorageProperties {
	return {
		name: '',
		diskType: 'zvol',
		rawPath: '',
		size: '',
		filesystemTarget: '',
		filesystemReadOnly: false,
		emulation: 'nvme',
		pool: defaultPool,
		bootOrder: null,
		loading: false
	};
}

export function deriveNameFromImage(filename: string): string {
	const withoutExtension = filename.replace(/\.(iso|img|raw|qcow2?|qed|vdi|vmdk|vpc|vhdx?)$/i, '');
	return (withoutExtension.trim() || 'disk').slice(0, 128);
}

export function emulationLabel(value: StorageEmulation): string {
	switch (value) {
		case 'ahci-cd':
			return 'AHCI CD-ROM';
		case 'ahci-hd':
			return 'AHCI Hard Disk';
		case 'virtio-blk':
			return 'VirtIO Block';
		case 'virtio-9p':
			return 'VirtIO 9P';
		case 'nvme':
			return 'NVMe';
	}
}

export function backingLabel(value: StorageDiskType): string {
	switch (value) {
		case 'zvol':
			return 'ZFS Volume';
		case 'raw':
			return 'RAW file';
		case 'image':
			return 'Downloader media';
		case 'filesystem':
			return 'ZFS filesystem';
	}
}
