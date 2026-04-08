import type { Jail, SimpleJail } from '$lib/types/jail/jail';
import type {
    CreateData,
    SimpleVm,
    VM,
    VMLifecycleAction,
    VMLifecycleBadgeStyle
} from '$lib/types/vm/vm';
import type { APIResponse } from '$lib/types/common';
import { toast } from 'svelte-sonner';
import { isValidIPv4, isValidIPv6, isValidVMName } from '../string';
import type { UTypeGroupedDownload } from '$lib/types/utilities/downloader';
import { type ClusterNode } from '$lib/types/cluster/cluster';
import { kvStorage } from '$lib/types/db';

export function isValidCreateData(
    modal: CreateData,
    utypeDownloads: UTypeGroupedDownload[]
): boolean {
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

    if (modal.storage.type === 'raw' || modal.storage.type === 'zvol') {
        if (!modal.storage.pool || modal.storage.pool.length < 1) {
            toast.error('No ZFS pool selected', toastConfig);
            return false;
        }

        if (!modal.storage.size || modal.storage.size < 1024 * 1024 * 128) {
            toast.error('Disk size must be >= 128 MiB', toastConfig);
            return false;
        }

        if (modal.storage.emulation === '') {
            toast.error('No emulation type selected', toastConfig);
            return false;
        }
    }

    if (modal.storage.iso === '') {
        toast.error(`Select 'none' if you don't want an installation media`, toastConfig);
        return false;
    }

    if (modal.network.switch !== '' && modal.network.switch.toLowerCase() !== 'none') {
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

    if (!isValidIPv4(modal.advanced.vncBind) && !isValidIPv6(modal.advanced.vncBind)) {
        toast.error('VNC bind IP must be a valid IPv4 or IPv6 address', toastConfig);
        return false;
    }

    if (modal.advanced.vncEnabled) {
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
    }

    if (
        (modal.advanced.cloudInit.data && !modal.advanced.cloudInit.metadata) ||
        (!modal.advanced.cloudInit.data && modal.advanced.cloudInit.metadata)
    ) {
        toast.error('Cloud-Init user and meta data required if enabled', toastConfig);
        return false;
    }

    if (modal.advanced.cloudInit.enabled) {
        if (!modal.advanced.cloudInit.data || !modal.advanced.cloudInit.metadata) {
            toast.error('Cloud-Init user and meta data required if enabled', toastConfig);
            return false;
        }

        if (modal.storage.iso === '' || modal.storage.iso.toLowerCase() === 'none') {
            toast.error('Cloud-Init requires installation media', toastConfig);
            return false;
        }

        const initImage = utypeDownloads.find(
            (download) => download.uType === 'cloud-init' && download.uuid === modal.storage.iso
        );
        if (!initImage) {
            toast.error('Selected installation media is not a valid Cloud-Init image', toastConfig);
            return false;
        }

        if (modal.storage.type === 'none') {
            toast.error('Cloud-Init requires a storage device', toastConfig);
            return false;
        }
    }

    return true;
}

function toVMCreateErrorText(error: APIResponse['error']): string {
    if (typeof error === 'string') {
        return error;
    }

    if (Array.isArray(error)) {
        return error.join(' ');
    }

    return '';
}

const vmCreateErrorMessageByCode: Record<string, string> = {
    cloud_init_data_missing: 'Cloud-Init requires both user-data and metadata',
    cloud_init_requires_iso: 'Cloud-Init requires an installation media image',
    cloud_init_requires_storage: 'Cloud-Init requires a VM storage device',
    invalid_cloud_init_yaml: 'Cloud-Init YAML is invalid. Verify user-data and metadata syntax',
    invalid_iso_or_image_format: 'Invalid or unsupported ISO/image format. Verify the selected media file',
    invalid_vnc_bind_ip: 'VNC bind IP must be a valid IPv4 or IPv6 address',
    invalid_vm_name: 'Invalid VM name. Use a valid hostname-style name',
    iso_or_image_not_found: 'Selected ISO/image could not be found or resolved on disk',
    unsupported_download_type:
        'Selected media source type is unsupported. Re-import the image as HTTP/path/torrent download',
    mac_object_already_in_use: 'Selected MAC object is already in use by another guest',
    media_not_cloud_init_capable: 'Selected media is not marked as cloud-init capable',
    no_emulation_type_selected: 'No storage emulation type selected',
    no_switch_emulation_type_selected: 'No network emulation type selected',
    pool_not_found: 'Selected storage pool was not found',
    rid_or_name_already_in_use: 'VM ID or name already exists. Choose a different value',
    storage_size_greater_than_available: 'Requested storage size is larger than available pool space',
    switch_not_found: 'Selected network switch was not found. Refresh and select a valid switch',
    vm_create_database_failure: 'Failed to persist VM metadata in the database',
    vm_create_dependency_not_ready:
        'Required VM dependencies are not ready (libvirt/ZFS/system services)',
    vm_create_runtime_failure: 'VM provisioning failed while applying runtime resources',
    vm_create_stale_artifacts_detected:
        'Stale VM artifacts were found for this ID. Clean up leftovers before retrying',
    vm_id_already_exists:
        'VM ID already exists in libvirt. Choose a different ID or clean up the existing domain',
    vnc_port_already_in_use_by_another_service:
        'VNC port is already used by another service. Choose a different port',
    vnc_port_already_in_use_by_another_vm:
        'VNC port is already used by another VM. Choose a different port'
};

export function getVMCreateErrorCode(response: Pick<APIResponse, 'message' | 'error'>): string {
    const backendCode =
        typeof response.message === 'string' ? response.message.trim().toLowerCase() : '';
    if (backendCode !== '' && backendCode !== 'failed_to_create') {
        return backendCode;
    }

    const errorText = toVMCreateErrorText(response.error).toLowerCase();
    if (errorText === '') {
        return 'failed_to_create_vm';
    }

    if (errorText.includes('exists=true, allowed=false')) {
        return 'invalid_iso_or_image_format';
    }

    if (errorText.includes('failed to define vm domain') && errorText.includes('already exists')) {
        return 'vm_id_already_exists';
    }

    const fallbackMatchers: Record<string, string> = {
        cloud_init_media_not_resolvable: 'iso_or_image_not_found',
        failed_to_create_lv_vm: 'vm_create_runtime_failure',
        failed_to_create_vm_with_associations: 'vm_create_database_failure',
        failed_to_fetch_iso_for_cloud_init_validation: 'iso_or_image_not_found',
        failed_to_find_download: 'iso_or_image_not_found',
        failed_to_find_iso: 'iso_or_image_not_found',
        failed_to_find_iso_by_uuid: 'iso_or_image_not_found',
        image_not_resolvable: 'iso_or_image_not_found',
        iso_or_img_not_found: 'iso_or_image_not_found'
    };

    for (const [needle, mappedCode] of Object.entries(fallbackMatchers)) {
        if (errorText.includes(needle)) {
            return mappedCode;
        }
    }

    return 'failed_to_create_vm';
}

export function getVMCreateErrorMessage(response: Pick<APIResponse, 'message' | 'error'>): string {
    const code = getVMCreateErrorCode(response);

    if (code.startsWith('no_pool_selected_for_')) {
        return 'No storage pool selected for the chosen storage type';
    }

    if (code.startsWith('size_should_be_at_least_')) {
        return 'Requested storage size is below the minimum supported size';
    }

    return vmCreateErrorMessageByCode[code] || 'Failed to create VM, check backend logs for details';
}

export function getNextId(vms: VM[] | SimpleVm[], jails: Jail[] | SimpleJail[]): number {
    const usedIds = [...vms.map((vm) => vm.rid), ...jails.map((jail) => jail.ctId)];
    if (usedIds.length === 0) return 100;
    return Math.max(...usedIds) + 1;
}

export function getNextGuestId(clusterNodes: ClusterNode[]): number {
    let maxId = 0;

    for (const node of clusterNodes) {
        if (Array.isArray(node.guestIDs) && node.guestIDs.length > 0) {
            const currentMax = Math.max(...node.guestIDs);
            if (currentMax > maxId) {
                maxId = currentMax;
            }
        }
    }

    return maxId === 0 ? 100 : maxId + 1;
}

export function generateCores(threadCount: number) {
    return Array.from({ length: threadCount }, (_, i) => {
        return {
            id: i + 1,
            status: Math.random() > 0.5 ? 'available' : 'busy'
        };
    });
}

export function getVMIconByGaId(id: string): string {
    switch (id) {
        case 'debian':
            return 'icon-[mdi--debian]';
        case 'openwrt':
            return 'icon-[simple-icons--openwrt]';
        case 'ubuntu':
            return 'icon-[mdi--ubuntu]';
        case 'fedora':
            return 'icon-[mdi--fedora]';
        case 'rhel':
            return 'icon-[mdi--redhat]';
        case 'centos':
            return 'icon-[mdi--centos]';
        case 'arch':
            return 'icon-[mdi--arch]';
        case 'alpine':
            return 'icon-[file-icons--alpine-linux]';
        case 'freebsd':
            return 'icon-[mdi--freebsd]';
        case 'openbsd':
            return 'icon-[file-icons--openbsd]';
        case 'mswindows':
            return 'icon-[ri--windows-fill]';
        case 'rocky':
            return 'icon-[simple-icons--rockylinux]';
        default:
            return 'icon-[carbon--unknown]';
    }

    return '';
}

export function vmStoragePools(vm: VM): string[] {
    const pools = new Set<string>();
    for (const storage of vm.storages || []) {
        const pool = (storage.pool || storage.dataset?.pool || '').trim();
        if (pool) {
            pools.add(pool);
        }
    }

    return [...pools];
}

export function vmBaseDataset(vm: VM): string {
    const pools = vmStoragePools(vm);
    if (pools.length > 0) {
        return `${pools[0]}/sylve/virtual-machines/${vm.rid}`;
    }

    for (const storage of vm.storages || []) {
        const datasetName = storage.dataset?.name || '';
        const match = datasetName.match(/^(.*\/virtual-machines\/\d+)(?:$|\/)/);
        if (match) {
            return match[1];
        }
    }

    return '';
}

const vmLifecycleBadgeStyles: Record<VMLifecycleAction, VMLifecycleBadgeStyle> = {
    start: {
        variant: 'outline',
        className: 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300',
        label: 'Start'
    },
    stop: {
        variant: 'outline',
        className: 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300',
        label: 'Stop'
    },
    shutdown: {
        variant: 'outline',
        className: 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300',
        label: 'Shutdown'
    },
    reboot: {
        variant: 'outline',
        className: 'border-blue-500/40 bg-blue-500/10 text-blue-700 dark:text-blue-300',
        label: 'Reboot'
    }
};

export type VMPendingLifecycleSnapshot = {
    initialStartedAt: string | null;
    observedNonRunningDuringReboot: boolean;
};

export function createVMPendingLifecycleSnapshot(
    currentDomainStatus: string,
    initialStartedAt?: string | null
): VMPendingLifecycleSnapshot {
    const normalizedStatus = String(currentDomainStatus || '').trim().toLowerCase();
    return {
        initialStartedAt: initialStartedAt ?? null,
        observedNonRunningDuringReboot: normalizedStatus !== 'running'
    };
}

export function markVMPendingSnapshotNonRunning(
    snapshot: VMPendingLifecycleSnapshot | null,
    normalizedDomainStatus: string,
    isDomainErrorState: boolean
): VMPendingLifecycleSnapshot | null {
    if (!snapshot) {
        return snapshot;
    }

    if (
        normalizedDomainStatus !== '' &&
        normalizedDomainStatus !== 'running' &&
        !isDomainErrorState &&
        !snapshot.observedNonRunningDuringReboot
    ) {
        return {
            ...snapshot,
            observedNonRunningDuringReboot: true
        };
    }

    return snapshot;
}

export function isVMPendingLifecycleActionSettled(
    pendingAction: VMLifecycleAction | '',
    snapshot: VMPendingLifecycleSnapshot | null,
    normalizedDomainStatus: string,
    isDomainErrorState: boolean,
    currentStartedAt?: string | null
): boolean {
    if (!pendingAction || !snapshot) {
        return false;
    }

    if (pendingAction === 'start') {
        return normalizedDomainStatus === 'running';
    }

    if (pendingAction === 'stop' || pendingAction === 'shutdown') {
        return (
            normalizedDomainStatus !== '' &&
            normalizedDomainStatus !== 'running' &&
            !isDomainErrorState
        );
    }

    if (pendingAction === 'reboot') {
        if (normalizedDomainStatus !== 'running') {
            return false;
        }

        const startedAtChanged = (currentStartedAt ?? null) !== snapshot.initialStartedAt;
        return snapshot.observedNonRunningDuringReboot || startedAtChanged;
    }

    return false;
}

export function getEffectiveVMLifecycleAction(
    activeAction: string,
    pendingAction: VMLifecycleAction | ''
): string {
    return activeAction || pendingAction;
}

export function getVMLifecyclePendingTimeoutMs(action: VMLifecycleAction): number {
    // Shutdown can legitimately take much longer before task state is observed.
    if (action === 'shutdown') {
        return 20000;
    }

    return 7000;
}

export function isVMLifecycleTransitionPending(
    pendingAction: VMLifecycleAction | '',
    hasActiveLifecycleTask: boolean
): boolean {
    return pendingAction !== '' && !hasActiveLifecycleTask;
}

export function shouldHideVMLifecycleButtons(
    hasActiveLifecycleTask: boolean,
    pendingAction: VMLifecycleAction | ''
): boolean {
    return hasActiveLifecycleTask || isVMLifecycleTransitionPending(pendingAction, hasActiveLifecycleTask);
}

export function getVMLifecycleBadgeStyle(action: string): VMLifecycleBadgeStyle {
    if (action in vmLifecycleBadgeStyles) {
        return vmLifecycleBadgeStyles[action as VMLifecycleAction];
    }

    return {
        variant: 'outline',
        className: 'text-muted-foreground',
        label: action ? action.charAt(0).toUpperCase() + action.slice(1) : 'Working'
    };
}

export function removeStaleCacheByRID(rid: number) {
    try {
        kvStorage.removeItem(`vm-${rid}`);
        kvStorage.removeItem(`vm-domain-${rid}`);
        kvStorage.removeItem(`vm-stats-${rid}`);
        kvStorage.removeItem(`vm-qga-${rid}`);
        kvStorage.removeItem(`vmDomain-${rid}`);
        kvStorage.removeItem(`vm-${rid}-snapshots`);
    } catch (e) {
        console.warn(`Error removing stale cache keys by RID ${rid}`, e)
    }
}
