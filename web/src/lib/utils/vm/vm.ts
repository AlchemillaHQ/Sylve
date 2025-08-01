import type { CreateData } from '$lib/types/vm/vm';
import { toast } from 'svelte-sonner';
import { isValidVMName } from '../string';

export function isValidCreateData(modal: CreateData): boolean {
	const toastConfig: Record<string, unknown> = {
		duration: 3000,
		position: 'bottom-center'
	};

	if (!isValidVMName(modal.name)) {
		toast.error('Invalid name', toastConfig);
		return false;
	}

	if (modal.id < 1 || modal.id > 9999) {
		toast.error('Invalid ID', toastConfig);
		return false;
	}

	if (modal.description && (modal.description.length < 1 || modal.description.length > 1024)) {
		toast.error('Invalid description', toastConfig);
	}

	if (modal.storage.type === 'raw') {
		if (!modal.storage.size || modal.storage.size < 1024 * 1024 * 128) {
			toast.error('Disk size must be >= 128 MiB', toastConfig);
		}
	}

	if (modal.storage.type === 'raw' || modal.storage.type === 'zvol') {
		if (!modal.storage.guid || modal.storage.guid.length < 1) {
			const noun = modal.storage.type === 'raw' ? 'filesystem' : 'volume';
			toast.error(`No ${noun} selected`, toastConfig);
			return false;
		}
	}

	if (modal.storage.emulation === '') {
		toast.error('No emulation type selected', toastConfig);
	}

	if (modal.storage.type === 'none' && modal.storage.iso === '') {
		toast.error('Atleast one disk or ISO must be selected', toastConfig);
		return false;
	}

	if (modal.network.switch !== 0) {
		if (modal.network.emulation === '') {
			toast.error('No network emulation type selected', toastConfig);
			return false;
		}
	}

	if (modal.hardware.sockets < 1) {
		toast.error('Sockets must be >= 1', toastConfig);
		return false;
	}

	if (modal.hardware.cores < 1) {
		toast.error('Cores must be >= 1', toastConfig);
		return false;
	}

	if (modal.hardware.threads < 1) {
		toast.error('Threads must be >= 1', toastConfig);
		return false;
	}

	if (modal.hardware.memory < 1024 * 1024 * 128) {
		toast.error('Memory must be >= 128 MiB', toastConfig);
		return false;
	}

	if (modal.advanced.vncPort < 1 || modal.advanced.vncPort > 65535) {
		toast.error('VNC port must be between 1 and 65535', toastConfig);
		return false;
	}

	if (modal.advanced.vncPassword && modal.advanced.vncPassword.length < 1) {
		toast.error('VNC password required', toastConfig);
		return false;
	}

	if (modal.advanced.vncResolution === '') {
		toast.error('No VNC resolution selected', toastConfig);
		return false;
	}

	return true;
}
