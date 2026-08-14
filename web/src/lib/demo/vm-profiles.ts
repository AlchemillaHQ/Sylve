import type { VM } from '$lib/types/vm/vm';

export type DemoVMProfileId = 'freebsd-i386' | 'tinycore-x86' | 'buildroot-x86';

type DemoVMImage = {
	url: string;
	size: number;
	async: boolean;
	useParts?: boolean;
	fixedChunkSize?: number;
};

type DemoVMEmulator =
	| {
			kind: 'hda';
			image: DemoVMImage;
			initialStateUrl?: string;
	  }
	| {
			kind: 'bzimage';
			image: DemoVMImage;
			cmdline: string;
	  };

export type DemoVMProfile = {
	id: DemoVMProfileId;
	label: string;
	release: string;
	architecture: 'i386' | 'x86';
	description: string;
	defaultName: string;
	memoryBytes: number;
	diskBytes: number;
	media: {
		uuid: string;
		fileName: string;
		url: string;
		size: number;
	};
	emulator: DemoVMEmulator;
};

const MIB = 1024 ** 2;
const GIB = 1024 ** 3;
const V86_IMAGE_HOST = 'https://i.copy.sh';

export const demoVMProfiles: readonly DemoVMProfile[] = [
	{
		id: 'freebsd-i386',
		label: 'FreeBSD',
		release: 'i386 browser image',
		architecture: 'i386',
		description: 'A prepared FreeBSD system with a resumable browser console.',
		defaultName: 'freebsd-lab',
		memoryBytes: 256 * MIB,
		diskBytes: 16 * GIB,
		media: {
			uuid: 'demo-freebsd-i386-disk',
			fileName: 'freebsd-i386-browser.img',
			url: `${V86_IMAGE_HOST}/freebsd/.img`,
			size: 2 * GIB
		},
		emulator: {
			kind: 'hda',
			image: {
				url: `${V86_IMAGE_HOST}/freebsd/.img`,
				size: 2 * GIB,
				async: true,
				useParts: true,
				fixedChunkSize: MIB
			},
			initialStateUrl: `${V86_IMAGE_HOST}/freebsd_state-v2.bin.zst`
		}
	},
	{
		id: 'tinycore-x86',
		label: 'Tiny Core Linux',
		release: '11.0 x86',
		architecture: 'x86',
		description: 'A compact Linux environment that boots from its installation image.',
		defaultName: 'edge-linux',
		memoryBytes: 128 * MIB,
		diskBytes: 8 * GIB,
		media: {
			uuid: 'demo-tinycore-11-x86-iso',
			fileName: 'TinyCore-11.0-x86.iso',
			url: `${V86_IMAGE_HOST}/TinyCore-11.0.iso`,
			size: 19_922_944
		},
		emulator: {
			kind: 'hda',
			image: {
				url: `${V86_IMAGE_HOST}/TinyCore-11.0.iso`,
				size: 19_922_944,
				async: false
			}
		}
	},
	{
		id: 'buildroot-x86',
		label: 'Buildroot Linux',
		release: '6.8 x86',
		architecture: 'x86',
		description: 'A minimal Linux worker image for an immediate shell and lifecycle demo.',
		defaultName: 'build-runner',
		memoryBytes: 256 * MIB,
		diskBytes: 4 * GIB,
		media: {
			uuid: 'demo-buildroot-68-x86-image',
			fileName: 'buildroot-linux-6.8-x86.img',
			url: `${V86_IMAGE_HOST}/buildroot-bzimage68.bin`,
			size: 10_068_480
		},
		emulator: {
			kind: 'bzimage',
			image: {
				url: `${V86_IMAGE_HOST}/buildroot-bzimage68.bin`,
				size: 10_068_480,
				async: false
			},
			cmdline:
				'tsc=reliable mitigations=off random.trust_cpu=on console=tty0 console=ttyS0,115200n8'
		}
	}
] as const;

export function getDemoVMProfile(id: string | null | undefined): DemoVMProfile | null {
	return demoVMProfiles.find((profile) => profile.id === id) ?? null;
}

export function getDemoVMProfileByMedia(uuid: string | null | undefined): DemoVMProfile | null {
	return demoVMProfiles.find((profile) => profile.media.uuid === uuid) ?? null;
}

export function resolveDemoVMProfile(vm: Pick<VM, 'name' | 'storages'>): DemoVMProfile | null {
	for (const storage of vm.storages) {
		const profile = getDemoVMProfileByMedia(storage.uuid);
		if (profile) return profile;
	}

	const name = vm.name.toLowerCase();
	if (name.includes('build')) return getDemoVMProfile('buildroot-x86');
	if (name.includes('tiny') || name.includes('edge')) return getDemoVMProfile('tinycore-x86');
	if (name.includes('freebsd')) return getDemoVMProfile('freebsd-i386');
	return null;
}
