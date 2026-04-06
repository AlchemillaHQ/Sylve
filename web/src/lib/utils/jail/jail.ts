import type {
    CreateData,
    JailLifecycleAction,
    JailLifecycleBadgeStyle
} from '$lib/types/jail/jail';
import type { APIResponse } from '$lib/types/common';
import { toast } from 'svelte-sonner';
import { isValidVMName } from '../string';

const DNS_PRESETS = {
    cloudflare: `nameserver 1.1.1.1
nameserver 1.0.0.1
nameserver 2606:4700:4700::1111
nameserver 2606:4700:4700::1001
`,
    google: `nameserver 8.8.8.8
nameserver 8.8.4.4
nameserver 2001:4860:4860::8888
nameserver 2001:4860:4860::8844
`,
    quad9: `nameserver 9.9.9.9
nameserver 149.112.112.112
nameserver 2620:fe::fe
nameserver 2620:fe::9
`
} as const;

export function validateMetadata(meta: string): boolean {
    if (meta.length === 0) {
        return true;
    }

    if (meta.length > 2048) {
        return false;
    }

    const lines = meta.split('\n');
    for (const line of lines) {
        const trimmed = line.trim();
        if (trimmed.length === 0) continue;

        const eqCount = (trimmed.match(/=/g) || []).length;
        if (eqCount !== 1) return false;

        const equalIndex = trimmed.indexOf('=');
        if (equalIndex <= 0 || equalIndex === trimmed.length - 1) {
            return false;
        }
    }

    return true;
}

export async function isValidCreateData(modal: CreateData): Promise<boolean> {
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

    if (modal.storage.pool.length < 1) {
        toast.error('No ZFS pool selected', toastConfig);
        return false;
    }

    if (modal.storage.base.length < 1 && modal.storage.bootstrapName.length < 1) {
        toast.error('No base selected', toastConfig);
        return false;
    }

    if (modal.network.switch.toLowerCase() !== 'none') {
        if (modal.advanced.jailType === 'linux') {
            if (modal.network.dhcp === true || modal.network.slaac === true) {
                toast.error('Linux jails cannot use DHCP or SLAAC', toastConfig);
                return false;
            }
        }

        if (modal.network.switch.toLowerCase() === 'inherit') {
            if (modal.network.inheritIPv4 === false && modal.network.inheritIPv6 === false) {
                toast.error('Either IPv4 or IPv6 must be inherited', toastConfig);
                return false;
            }
        }
    }

    if (modal.advanced.metadata.env.length > 2048 || modal.advanced.metadata.meta.length > 2048) {
        toast.error('Metadata too long', toastConfig);
        return false;
    }

    if (
        !validateMetadata(modal.advanced.metadata.env) ||
        !validateMetadata(modal.advanced.metadata.meta)
    ) {
        toast.error('Invalid metadata format', toastConfig);
        return false;
    }

    return true;
}

function toJailCreateErrorText(error: APIResponse['error']): string {
    if (typeof error === 'string') {
        return error;
    }

    if (Array.isArray(error)) {
        return error.join(' ');
    }

    return '';
}

const jailCreateErrorMessageByCode: Record<string, string> = {
    base_is_not_a_directory:
        'Selected base image path is not a directory. Re-extract the base/rootfs and retry.',
    base_path_does_not_exist: 'Selected base image could not be found on disk.',
    download_uuid_required: 'A base image is required to create a jail.',
    invalid_ct_id: 'Invalid jail ID. Use a value between 1 and 9999.',
    invalid_hostname: 'Invalid hostname.',
    invalid_ipv4_gateway: 'Invalid IPv4 gateway selection.',
    invalid_ipv6_gateway: 'Invalid IPv6 gateway selection.',
    invalid_jail_allowed_options: 'One or more allowed options are invalid.',
    jail_base_fs_with_ctid_already_exists:
        'A jail root dataset already exists for this CTID. Clean up leftovers before retrying.',
    jail_create_database_failure: 'Failed to persist jail metadata in the database.',
    jail_create_dependency_not_ready:
        'Required jail dependencies are not ready (system/ZFS services).',
    jail_create_runtime_failure: 'Jail provisioning failed while applying runtime resources.',
    jail_create_stale_artifacts_detected:
        'Stale jail artifacts were found for this CTID. Clean up leftovers before retrying.',
    jail_with_ctid_already_exists:
        'Jail ID already exists. Choose a different ID or remove the existing jail.',
    linux_jails_cannot_use_dhcp_or_slaac: 'Linux jails cannot use DHCP or SLAAC.',
    mac_already_used: 'Selected MAC object is already in use.',
    pool_not_found: 'Selected storage pool was not found.',
    standard_switch_not_found: 'Selected network switch was not found.',
    switch_name_required: 'Network switch selection is required.'
};

export function getJailCreateErrorCode(response: Pick<APIResponse, 'message' | 'error'>): string {
    const backendCode =
        typeof response.message === 'string' ? response.message.trim().toLowerCase() : '';
    if (backendCode !== '' && backendCode !== 'failed_to_create') {
        return backendCode;
    }

    const errorText = toJailCreateErrorText(response.error).toLowerCase();
    if (errorText === '') {
        return 'failed_to_create_jail';
    }

    if (errorText.includes('jail_with_ctid_') && errorText.includes('already_exists')) {
        return 'jail_with_ctid_already_exists';
    }

    const fallbackMatchers: Record<string, string> = {
        failed_to_begin_tx: 'jail_create_database_failure',
        failed_to_commit_tx: 'jail_create_database_failure',
        failed_to_create_jail: 'jail_create_runtime_failure',
        failed_to_create_jail_config: 'jail_create_runtime_failure',
        failed_to_create_jail_dataset: 'jail_create_runtime_failure',
        failed_to_create_network: 'jail_create_runtime_failure',
        failed_to_find_base: 'base_path_does_not_exist'
    };

    for (const [needle, mappedCode] of Object.entries(fallbackMatchers)) {
        if (errorText.includes(needle)) {
            return mappedCode;
        }
    }

    return 'failed_to_create_jail';
}

export function getJailCreateErrorMessage(response: Pick<APIResponse, 'message' | 'error'>): string {
    const code = getJailCreateErrorCode(response);
    return (
        jailCreateErrorMessageByCode[code] ||
        'Failed to create jail. Check backend logs for details.'
    );
}

export function generateSimpleLinuxFSTab(ctId: number, pool: string): string {
    const base = `/${pool}/sylve/jails/${ctId}`;

    const entries = [
        { fs: 'devfs', mp: `${base}/dev`, type: 'devfs', opts: 'rw' },
        { fs: 'tmpfs', mp: `${base}/dev/shm`, type: 'tmpfs', opts: 'rw,size=1g,mode=1777' },
        { fs: 'fdescfs', mp: `${base}/dev/fd`, type: 'fdescfs', opts: 'rw,linrdlnk' },
        { fs: 'linprocfs', mp: `${base}/proc`, type: 'linprocfs', opts: 'rw' },
        { fs: 'linsysfs', mp: `${base}/sys`, type: 'linsysfs', opts: 'rw' }
    ];

    return entries.map((e) => `${e.fs}\t${e.mp}\t${e.type}\t${e.opts}\t0\t0`).join('\n') + '\n';
}

export function dnsConfigPresets(
    resolver: keyof typeof DNS_PRESETS
): string {
    return DNS_PRESETS[resolver];
}

const jailLifecycleBadgeStyles: Record<JailLifecycleAction, JailLifecycleBadgeStyle> = {
    start: {
        variant: 'outline',
        className: 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300',
        label: 'Start'
    },
    stop: {
        variant: 'outline',
        className: 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300',
        label: 'Stop'
    }
};

export function getEffectiveJailLifecycleAction(
    activeAction: string,
    pendingAction: JailLifecycleAction | ''
): string {
    return activeAction || pendingAction;
}

export function isJailPendingLifecycleActionSettled(
    pendingAction: JailLifecycleAction | '',
    currentState: string | null | undefined
): boolean {
    if (!pendingAction || !currentState) {
        return false;
    }

    if (pendingAction === 'start') {
        return currentState === 'ACTIVE';
    }

    if (pendingAction === 'stop') {
        return currentState === 'INACTIVE';
    }

    return false;
}

export function getJailLifecyclePendingTimeoutMs(_action: JailLifecycleAction): number {
    return 7000;
}

export function isJailLifecycleTransitionPending(
    pendingAction: JailLifecycleAction | '',
    hasActiveLifecycleTask: boolean
): boolean {
    return pendingAction !== '' && !hasActiveLifecycleTask;
}

export function shouldHideJailLifecycleButtons(
    hasActiveLifecycleTask: boolean,
    pendingAction: JailLifecycleAction | ''
): boolean {
    return hasActiveLifecycleTask || isJailLifecycleTransitionPending(pendingAction, hasActiveLifecycleTask);
}

export function getJailLifecycleBadgeStyle(action: string): JailLifecycleBadgeStyle {
    if (action in jailLifecycleBadgeStyles) {
        return jailLifecycleBadgeStyles[action as JailLifecycleAction];
    }

    return {
        variant: 'outline',
        className: 'text-muted-foreground',
        label: action ? action.charAt(0).toUpperCase() + action.slice(1) : 'Working'
    };
}
