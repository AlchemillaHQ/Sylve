export {
	formatSizeBytes as formatSize,
	parseSizeInputToBytes as parseSize,
	parseSizeInputToBytes as parseSwapSize
} from './size';

export type DiskKind = 'nvme' | 'ssd' | 'hdd' | 'virtual' | 'sdcard' | 'other';

export type PoolPartition = {
	name?: string;
	index: number;
	type: string;
	label?: string;
	startBytes?: number;
	sizeBytes: number;
};

export type PoolDisk = {
	device: string;
	sizeBytes: number;
	kind: DiskKind;
	model?: string;
	serial?: string;
	rotation?: string;
	scheme?: string;
	partitions?: PoolPartition[];
	boot?: boolean;
};

export type ApiDisk = {
	name: string;
	sizeBytes: number;
	sectorSize?: number;
	description?: string;
	serial?: string;
	rotationRate?: string;
	kind?: string;
	boot?: boolean;
	scheme?: string;
	partitions?: PoolPartition[];
};

export type RaidType = 'stripe' | 'mirror' | 'raidz1' | 'raidz2' | 'raidz3' | 'raid10';

export const MIN_TARGET_BYTES = 8 * 1024 ** 3;

export const RAID_TYPES: { value: RaidType; label: string; min: number; even?: boolean }[] = [
	{ value: 'stripe', label: 'Stripe', min: 1 },
	{ value: 'mirror', label: 'Mirror', min: 2 },
	{ value: 'raidz1', label: 'RAID-Z1', min: 3 },
	{ value: 'raidz2', label: 'RAID-Z2', min: 4 },
	{ value: 'raidz3', label: 'RAID-Z3', min: 5 },
	{ value: 'raid10', label: 'RAID-10', min: 4, even: true }
];

export type PoolVdev = {
	id: string;
	raid: RaidType;
	disks: string[];
};

export type PoolProperty = { key: string; value: string };

export type PoolConfig = {
	name: string;
	vdevs: PoolVdev[];
	compression: string;
	ashift: string;
	autotrim: boolean;
	comment: string;
	properties: PoolProperty[];
};

let vdevSeq = 0;

export function newVdev(raid: RaidType = 'stripe', disks: string[] = []): PoolVdev {
	return { id: `vdev-${++vdevSeq}`, raid, disks };
}

export function defaultPool(): PoolConfig {
	return {
		name: 'zroot',
		vdevs: [newVdev()],
		compression: 'lz4',
		ashift: '12',
		autotrim: false,
		comment: '',
		properties: []
	};
}

export const DISK_KINDS: DiskKind[] = ['nvme', 'ssd', 'hdd', 'virtual', 'sdcard', 'other'];

export const DISK_LABELS: Record<DiskKind, string> = {
	nvme: 'NVMe',
	ssd: 'SSD',
	hdd: 'HDD',
	virtual: 'Virtual',
	sdcard: 'SD card',
	other: 'Other'
};

export function diskLabel(kind: string): string {
	return DISK_LABELS[kind as DiskKind] ?? kind;
}

export function partitionTypeLabel(type: string): string {
	switch (type) {
		case 'freebsd-zfs':
			return 'ZFS';
		case 'freebsd-ufs':
			return 'UFS';
		case 'freebsd-swap':
			return 'Swap';
		case 'freebsd-boot':
			return 'Boot';
		case 'efi':
			return 'EFI';
		case 'freebsd':
			return 'FreeBSD';
		default:
			return type;
	}
}

export function kindOf(device: string, description = ''): DiskKind {
	const text = `${device} ${description}`.toLowerCase();
	if (text.includes('nvme') || text.includes('nvd') || text.includes('nda')) return 'nvme';
	if (text.includes('vtbd') || text.includes('virtio') || text.includes('virtual')) return 'virtual';
	if (text.includes('mmcsd')) return 'sdcard';
	if (text.includes('ssd') || text.includes('solid state')) return 'ssd';
	return 'hdd';
}

export function toPoolDisk(disk: ApiDisk): PoolDisk {
	const kind = (disk.kind as DiskKind) || kindOf(disk.name, disk.description ?? '');
	return {
		device: `/dev/${disk.name}`,
		sizeBytes: disk.sizeBytes,
		kind,
		model: disk.description ?? '',
		serial: disk.serial ?? '',
		rotation: disk.rotationRate ?? '',
		scheme: disk.scheme ?? '',
		partitions: disk.partitions ?? [],
		boot: disk.boot ?? false
	};
}

export function isPartitionMember(member: string): boolean {
	return /[ps]\d+$/.test(shortDevice(member));
}

export function partitionParent(member: string): string {
	return shortDevice(member).replace(/[ps]\d+$/, '');
}

export function isEligiblePartition(part: PoolPartition): boolean {
	return part.type === 'freebsd-zfs';
}

export function memberBytes(poolDisks: PoolDisk[], member: string): number {
	const dev = shortDevice(member);
	if (!isPartitionMember(dev)) {
		return poolDisks.find((disk) => shortDevice(disk.device) === dev)?.sizeBytes ?? 0;
	}
	const parent = poolDisks.find((disk) => shortDevice(disk.device) === partitionParent(dev));
	if (!parent?.partitions) return 0;
	const index = Number(dev.match(/(\d+)$/)?.[1] ?? -1);
	return (
		parent.partitions.find((part) => part.name === dev)?.sizeBytes ??
		parent.partitions.find((part) => part.index === index)?.sizeBytes ??
		0
	);
}

export function shortDevice(device: string): string {
	return device.replace(/^\/dev\//, '');
}

export function formatBytes(bytes: number): string {
	if (!bytes) return '—';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
	let value = bytes;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit += 1;
	}
	const decimals = value < 10 && unit > 1 ? 1 : 0;
	return `${decimals ? value.toFixed(decimals) : Math.round(value)} ${units[unit]}`;
}

const MIB = 1024 ** 2;
const MIN_ZFS_MEMBER_BYTES = 64 * MIB;
const alignLayout = (bytes: number) => Math.ceil(bytes / MIB) * MIB;

export type StorageLayout = { swapBytes: number; encrypted: boolean };

function wholeDiskDataBytes(
	disk: PoolDisk,
	swapBytes: number,
	bootpool: boolean,
	encrypted: boolean
): number {
	if (!Number.isSafeInteger(disk.sizeBytes) || !Number.isSafeInteger(swapBytes) || swapBytes < 0)
		return 0;
	const reserved =
		2 * MIB + MIB + 260 * MIB + alignLayout(swapBytes) +
		(bootpool ? 2 * 1024 ** 3 : 0) + (encrypted ? 4096 : 0);
	return Math.max(0, disk.sizeBytes - reserved);
}

export function ufsRootBytes(
	disk: PoolDisk,
	target: 'entire' | 'free',
	swapBytes: number,
	encrypted: boolean
): number {
	if (target === 'entire') return wholeDiskDataBytes(disk, swapBytes, false, encrypted);
	if (
		disk.scheme !== 'GPT' || !Number.isSafeInteger(disk.sizeBytes) ||
		!Number.isSafeInteger(swapBytes) || swapBytes < 0
	) return 0;
	const parts = [...(disk.partitions ?? [])].sort((a, b) => (a.startBytes ?? 0) - (b.startBytes ?? 0));
	const end = Math.floor((disk.sizeBytes - MIB) / MIB) * MIB;
	let previousEnd = 0;
	let largest = 0;
	let hasEFI = false;
	let hasBoot = false;
	const consider = (stop: number) => {
		const start = alignLayout(Math.max(MIB, previousEnd));
		largest = Math.max(largest, Math.floor(Math.min(stop, end) / MIB) * MIB - start);
	};
	for (const part of parts) {
		const start = part.startBytes ?? 0;
		if (
			!Number.isSafeInteger(start) || !Number.isSafeInteger(part.sizeBytes) ||
			start <= 0 || part.sizeBytes <= 0 || start < previousEnd ||
			start + part.sizeBytes > disk.sizeBytes ||
			part.label === 'rootfs' || part.label === 'swap0'
		) return 0;
		consider(start);
		previousEnd = start + part.sizeBytes;
		hasEFI ||= part.type === 'efi';
		hasBoot ||= part.type === 'freebsd-boot';
	}
	consider(end);
	if (parts.some((part) => (!hasEFI && part.label === 'efi0') || (!hasBoot && part.label === 'boot0')))
		return 0;
	const fixed =
		(hasEFI ? 0 : 260 * MIB) + (hasBoot ? 0 : MIB) +
		alignLayout(swapBytes) + (encrypted ? 4096 : 0);
	return Math.max(0, largest - fixed);
}

function memberDataBytes(poolDisks: PoolDisk[], member: string, layout?: StorageLayout): number {
	let bytes: number;
	if (layout && !isPartitionMember(member)) {
		const disk = poolDisks.find((disk) => shortDevice(disk.device) === shortDevice(member));
		bytes = disk ? wholeDiskDataBytes(disk, layout.swapBytes, layout.encrypted, layout.encrypted) : 0;
	} else {
		bytes = memberBytes(poolDisks, member);
	}
	return layout && bytes < MIN_ZFS_MEMBER_BYTES ? 0 : bytes;
}

export function vdevUsableBytes(poolDisks: PoolDisk[], vdev: PoolVdev, layout?: StorageLayout): number {
	const sizes = vdev.disks.map((member) => memberDataBytes(poolDisks, member, layout));
	if (!raidReady(vdev.raid, sizes.length) || sizes.some((size) => size <= 0)) return 0;
	const smallest = Math.min(...sizes);
	const total = sizes.reduce((sum, size) => sum + size, 0);
	switch (vdev.raid) {
		case 'mirror':
			return smallest;
		case 'raidz1':
			return smallest * (sizes.length - 1);
		case 'raidz2':
			return smallest * (sizes.length - 2);
		case 'raidz3':
			return smallest * (sizes.length - 3);
		case 'raid10': {
			let usable = 0;
			for (let i = 0; i + 1 < sizes.length; i += 2) usable += Math.min(sizes[i], sizes[i + 1]);
			return usable;
		}
		default:
			return total;
	}
}

export function usableBytes(poolDisks: PoolDisk[], config: PoolConfig, layout?: StorageLayout): number {
	const capacities = config.vdevs.map((vdev) => vdevUsableBytes(poolDisks, vdev, layout));
	if (capacities.some((size) => size <= 0)) return 0;
	return capacities.reduce((sum, size) => sum + size, 0);
}

export function raidLabel(raid: RaidType): string {
	return RAID_TYPES.find((type) => type.value === raid)?.label ?? raid;
}

export function raidRequirement(raid: RaidType): string {
	const spec = RAID_TYPES.find((type) => type.value === raid);
	if (!spec) return '';
	return spec.even
		? `needs ${spec.min} or more disks (in pairs)`
		: `needs at least ${spec.min} disk${spec.min > 1 ? 's' : ''}`;
}

export function raidReady(raid: RaidType, diskCount: number): boolean {
	const spec = RAID_TYPES.find((type) => type.value === raid);
	if (!spec) return false;
	if (diskCount < spec.min) return false;
	if (spec.even && diskCount % 2 !== 0) return false;
	return true;
}

const RESERVED_POOL_WORDS = ['log', 'mirror', 'raidz', 'spare'];

export function poolNameProblem(name: string): string | null {
	const value = name.trim();
	if (!value) return 'Enter a pool name';
	if (value === 'bootpool') return 'Reserved for the installer bootpool';
	if (!/^[a-zA-Z]/.test(value)) return 'Must start with a letter';
	if (!/^[a-zA-Z0-9_.-]+$/.test(value))
		return 'Use letters, digits, dot, underscore or hyphen only';
	const reserved = RESERVED_POOL_WORDS.find((word) => value.startsWith(word));
	if (reserved) return `Cannot start with "${reserved}"`;
	if (/^c[0-9]/.test(value)) return 'Cannot start with c followed by a digit';
	return null;
}

export function configDisks(config: PoolConfig): string[] {
	return config.vdevs.flatMap((vdev) => vdev.disks);
}

export function memberConflict(config: PoolConfig): string | null {
	const members = configDisks(config).map(shortDevice);
	const parents = new Set(members.filter(isPartitionMember).map(partitionParent));
	const seen = new Set<string>();
	for (const name of members) {
		if (seen.has(name)) return `${name} is selected twice`;
		if (!isPartitionMember(name) && parents.has(name)) {
			return `${name} and its partition cannot both join the pool`;
		}
		seen.add(name);
	}
	return null;
}

export function poolReady(config: PoolConfig): boolean {
	return (
		poolNameProblem(config.name) === null && config.vdevs.length > 0 &&
		poolBootProblem(config) === null &&
		config.vdevs.every((vdev) => raidReady(vdev.raid, vdev.disks.length))
	);
}

export function poolBootProblem(config: PoolConfig): string | null {
	const conflict = memberConflict(config);
	if (conflict) return conflict;
	const members = configDisks(config);
	if (members.length > 0 && members.every(isPartitionMember)) {
		return 'At least one whole disk is required for the boot loader.';
	}
	return null;
}

export function vdevSummary(config: PoolConfig): string {
	return config.vdevs
		.map((vdev) => {
			const disks = vdev.disks.length ? vdev.disks.map(shortDevice).join('+') : 'no disks';
			return `${raidLabel(vdev.raid)} (${disks})`;
		})
		.join(' + ');
}

export function poolLayoutLabel(config: PoolConfig): string {
	if (config.vdevs.length === 1) return raidLabel(config.vdevs[0].raid);
	return `${config.vdevs.length} VDEVs`;
}

export type PartitioningMode = 'zfs' | 'ufs';

export function suggestUfsSwap(availableBytes: number): number {
	const free = Math.max(0, availableBytes);
	return Math.min(Math.floor(free / 20), 4 * 1024 ** 3);
}
