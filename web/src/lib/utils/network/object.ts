import type { ListsType, NetworkObject } from '$lib/types/network/object';
import { isValidIPv4, isValidIPv6 } from '../string';

export function generateIPOptions(
    networkObjects: NetworkObject[] | undefined,
    type: string,
    markValue: boolean = false
): { label: string; value: string }[] {
    if (!networkObjects || networkObjects.length === 0) {
        return [];
    }

    const options = [] as { label: string; value: string }[];
    const objects = networkObjects?.filter((obj) => obj.type === 'Host');
    if (!objects || objects.length === 0) {
        return [];
    }

    for (const object of objects) {
        if (object.entries && object.entries.length === 1) {
            for (const entry of object.entries) {
                const validator = type.toLowerCase() == 'ipv4' ? isValidIPv4 : isValidIPv6;
                if (validator(entry.value)) {
                    options.push({
                        label: `${object.name} (${entry.value})`,
                        value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
                    });
                }
            }
        }
    }

    return options;
}

export function generateNetworkOptions(
    networkObjects: NetworkObject[] | undefined,
    type: string,
    markValue: boolean = false
): { label: string; value: string }[] {
    if (!networkObjects || networkObjects.length === 0) {
        return [];
    }

    const options = [] as { label: string; value: string }[];
    const objects = networkObjects?.filter((obj) => obj.type === 'Network');
    if (!objects || objects.length === 0) {
        return [];
    }

    for (const object of objects) {
        if (object.entries && object.entries.length > 0) {
            for (const entry of object.entries) {
                if (type.toLowerCase() === 'ipv4' && isValidIPv4(entry.value, true)) {
                    options.push({
                        label: `${object.name} (${entry.value})`,
                        value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
                    });
                } else if (type.toLowerCase() === 'ipv6' && isValidIPv6(entry.value, true)) {
                    options.push({
                        label: `${object.name} (${entry.value})`,
                        value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
                    });
                }
            }
        }
    }

    return options;
}

export function generateMACOptions(
    networkObjects: NetworkObject[] | undefined,
    markValue: boolean = false
): { label: string; value: string }[] {
    if (!networkObjects || networkObjects.length === 0) {
        return [];
    }

    const options = [] as { label: string; value: string }[];
    const objects = networkObjects?.filter((obj) => obj.type === 'Mac');
    if (!objects || objects.length === 0) {
        return [];
    }

    for (const object of objects) {
        if (object.entries && object.entries.length > 0) {
            for (const entry of object.entries) {
                options.push({
                    label: `${object.name} (${entry.value})`,
                    value: markValue ? `mac-${object.id.toString()}` : object.id.toString()
                });
            }
        }
    }

    return options;
}

export function generateDUIDOptions(
    networkObjects: NetworkObject[] | undefined,
    markValue: boolean = false
): { label: string; value: string }[] {
    if (!networkObjects || networkObjects.length === 0) {
        return [];
    }
    const options = [] as { label: string; value: string }[];
    const objects = networkObjects?.filter((obj) => obj.type === 'DUID');
    if (!objects || objects.length === 0) {
        return [];
    }

    for (const object of objects) {
        if (object.entries && object.entries.length > 0) {
            for (const entry of object.entries) {
                options.push({
                    label: `${object.name} (${entry.value})`,
                    value: markValue ? `duid-${object.id.toString()}` : object.id.toString()
                });
            }
        }
    }

    return options;
}

export function objectLists(t: ListsType): { label: string; value: string }[] {
    switch (t) {
        case 'firehol':
            return [
                {
                    label: 'FireHOL Level 1 (Safe)',
                    value: 'https://iplists.firehol.org/files/firehol_level1.netset'
                },
                {
                    label: 'FireHOL Level 2 (Balanced)',
                    value: 'https://iplists.firehol.org/files/firehol_level2.netset'
                },
                {
                    label: 'FireHOL Level 3 (Paranoid)',
                    value: 'https://iplists.firehol.org/files/firehol_level3.netset'
                },
            ]
        case 'cloudflare':
            return [
                {
                    label: 'Cloudflare IPs (IPv4)',
                    value: 'https://www.cloudflare.com/ips-v4'
                },
                {
                    label: 'Cloudflare IPs (IPv6)',
                    value: 'https://www.cloudflare.com/ips-v6'
                }
            ]
        case 'abusedb':
            return [
                {
                    label: 'AbuseDB (1 Day)',
                    value: 'https://iplists.firehol.org/files/abuseipdb_1d.ipset'
                },
                {
                    label: 'AbuseDB (30 Days)',
                    value: 'https://iplists.firehol.org/files/abuseipdb_30d.ipset'
                }
            ]
        default:
            return [];
    }
}