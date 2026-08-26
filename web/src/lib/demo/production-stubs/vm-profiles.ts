import type { VM } from '$lib/types/vm/vm';

export type DemoVMProfileId = 'unavailable';

export type DemoVMProfile = {
	id: DemoVMProfileId;
	label: string;
	release: string;
	architecture: 'x86';
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
	emulator: {
		kind: 'hda';
		image: { url: string; size: number; async: false };
	};
};

const unavailableProfile: DemoVMProfile = {
	id: 'unavailable',
	label: '',
	release: '',
	architecture: 'x86',
	description: '',
	defaultName: '',
	memoryBytes: 0,
	diskBytes: 0,
	media: { uuid: '', fileName: '', url: '', size: 0 },
	emulator: { kind: 'hda', image: { url: '', size: 0, async: false } }
};

export const demoVMProfiles: readonly DemoVMProfile[] = [unavailableProfile];

export function getDemoVMProfile(_id: string | null | undefined): DemoVMProfile {
	return unavailableProfile;
}

export function getDemoVMProfileByMedia(_uuid: string | null | undefined): DemoVMProfile | null {
	return null;
}

export function resolveDemoVMProfile(_vm: Pick<VM, 'name' | 'storages'>): DemoVMProfile | null {
	return null;
}
