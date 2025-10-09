import type { CreateData } from '$lib/types/jail/jail';
import { toast } from 'svelte-sonner';
import { isValidVMName } from '../string';
import { doesPathHaveBase } from '$lib/api/system/file-explorer';
import type { Dataset } from '$lib/types/zfs/dataset';

export async function isValidCreateData(
	modal: CreateData,
	filesystems: Dataset[]
): Promise<boolean> {
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
		return false;
	}

	if (modal.storage.dataset.length < 1) {
		toast.error('No storage dataset selected', toastConfig);
		return false;
	}

	if (modal.storage.base.length < 1) {
		const fs = filesystems.find((f) => f.guid === modal.storage.dataset);
		if (!fs) {
			toast.error('Selected dataset not found', toastConfig);
			return false;
		}

		const hasBase = await doesPathHaveBase(fs.mountpoint);
		if (!hasBase) {
			toast.error('No system base selected', toastConfig);
			return false;
		}
	}

	if (modal.network.switch == -1) {
		if (modal.network.inheritIPv4 === false && modal.network.inheritIPv6 === false) {
			toast.error('Either IPv4 or IPv6 must be inherited', toastConfig);
			return false;
		}
	}

	return true;
}
