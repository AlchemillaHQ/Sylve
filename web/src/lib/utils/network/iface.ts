import type { Column, Row } from '$lib/types/components/tree-table';
import type { Iface } from '$lib/types/network/iface';
import { generateNumberFromString } from '../numbers';

function ipv4NetmaskToPrefix(netmask?: string | null): number | null {
    if (!netmask) {
        return null;
    }

    const parts = netmask.split('.').map((p) => Number.parseInt(p, 10));
    if (parts.length !== 4 || parts.some((p) => Number.isNaN(p) || p < 0 || p > 255)) {
        return null;
    }

    let prefix = 0;
    for (const part of parts) {
        let n = part;
        for (let i = 0; i < 8; i++) {
            if ((n & 0x80) === 0x80) {
                prefix++;
            }
            n <<= 1;
        }
    }

    return prefix;
}

function formatIPv4(iface: Iface): string {
    if (!iface.ipv4 || iface.ipv4.length === 0) {
        return '-';
    }

    return iface.ipv4
        .map((addr) => {
            const prefix = ipv4NetmaskToPrefix(addr.netmask);
            const suffix = prefix !== null ? `/${prefix}` : '';
            return `${addr.ip}${suffix}`;
        })
        .join('\n');
}

function formatIPv6(iface: Iface): string {
    if (!iface.ipv6 || iface.ipv6.length === 0) {
        return '-';
    }

    return iface.ipv6
        .map((addr) => {
            const suffix = addr.prefixLength !== undefined ? `/${addr.prefixLength}` : '';
            return `${addr.ip}${suffix}`;
        })
        .join('\n');
}

function getIPv4Details(iface: Iface): Record<string, string> | null {
    if (!iface.ipv4 || iface.ipv4.length === 0) {
        return null;
    }

    const details: Record<string, string> = {};

    iface.ipv4.forEach((addr, index) => {
        const prefix = ipv4NetmaskToPrefix(addr.netmask);
        const cidr = prefix !== null ? `${addr.ip}/${prefix}` : addr.ip || '-';
        details[`Address ${index + 1}`] = `${cidr} | Netmask: ${addr.netmask || '-'} | Broadcast: ${addr.broadcast || '-'}`;
    });

    return details;
}

function getIPv6Details(iface: Iface): Record<string, string> | null {
    if (!iface.ipv6 || iface.ipv6.length === 0) {
        return null;
    }

    const details: Record<string, string> = {};

    iface.ipv6.forEach((addr, index) => {
        const prefix =
            addr.prefixLength !== undefined && addr.prefixLength !== null ? `/${addr.prefixLength}` : '';
        details[`Address ${index + 1}`] =
            `${addr.ip || '-'}${prefix} | Scope: ${addr.scopeId} | Auto: ${addr.autoConf ? 'true' : 'false'} | ` +
            `Detached: ${addr.detached ? 'true' : 'false'} | Deprecated: ${addr.deprecated ? 'true' : 'false'} | ` +
            `Preferred: ${addr.lifeTimes?.preferred ?? '-'} | Valid: ${addr.lifeTimes?.valid ?? '-'}`;
    });

    return details;
}

export function generateTableData(
    columns: Column[],
    interfaces: Iface[]
): {
    rows: Row[];
    columns: Column[];
} {
    const rows: Row[] = [];
    for (const iface of interfaces) {
        let isBridge = false;
        let isEpair = false;
        let isTap = false;
        let model = iface.model;

        if (iface.groups) {
            if (iface.groups.includes('bridge')) {
                isBridge = true;
                model = 'Bridge';
            }

            if (iface.groups.includes('epair')) {
                isEpair = true;
                model = 'Epair';
            }

            if (iface.groups.includes('tap')) {
                isTap = true;
                model = 'TAP';
            }

            if (iface.groups.includes('wg')) {
                model = 'WireGuard';
            }

            if (iface.groups.includes('tun') && iface.name.startsWith('tailscale')) {
                model = 'Tailscale';
            }
        }

        if (iface.description.startsWith('svm-vlan')) {
            continue;
        }

        const row: Row = {
            id: generateNumberFromString(iface.ether + iface.name),
            ether: iface.ether,
            hwaddr: iface.hwaddr,
            name: iface.name,
            model: model,
            description: iface.description,
            ipv4: formatIPv4(iface),
            ipv6: formatIPv6(iface),
            metric: iface.metric,
            mtu: iface.mtu,
            media: iface.media,
            isBridge: isBridge,
            isEpair: isEpair,
            isTap: isTap
        };

        rows.push(row);
    }

    return {
        rows,
        columns: columns
    };
}

type CleanIfaceData = {
    Name: string;
    Description: string;
    Model: string;
    'MAC Address': string;
    'IPv4 Addresses'?: Record<string, string>;
    'IPv6 Addresses'?: Record<string, string>;
    MTU: number | null | undefined;
    Metric: number | null | undefined;
    Flags: {
        Raw: number;
        Description: string;
    };
    'Enabled Capabilities': {
        Raw: number;
        Description: string;
    };
    'Supported Capabilities': {
        Raw: number;
        Description: string;
    };
    'Media Options'?: {
        Status: string;
        Type: string;
        'Sub Type': string;
        Mode: string;
        Options: string;
    };
};

export function getCleanIfaceData(iface: Iface): CleanIfaceData {
    let displayName = iface.name;
    let model = iface.model;

    if (iface.groups) {
        if (iface.groups.includes('bridge')) {
            model = 'Bridge';
            displayName = `${iface.description} (${iface.name})`;
        }
    }

    const obj: CleanIfaceData = {
        ['Name']: displayName,
        ['Description']: iface.description || '-',
        ['Model']: model ? model : '-',
        ['MAC Address']: iface.ether || '-',
        ['MTU']: iface.mtu,
        ['Metric']: iface.metric,
        ['Flags']: {
            ['Raw']: iface.flags.raw,
            ['Description']: iface.flags.desc?.join(', ') || '-'
        },
        ['Enabled Capabilities']: {
            ['Raw']: iface.capabilities.enabled.raw,
            ['Description']: iface.capabilities.enabled.desc?.join(', ') || '-'
        },
        ['Supported Capabilities']: {
            ['Raw']: iface.capabilities.supported.raw,
            ['Description']: iface.capabilities.supported.desc?.join(', ') || '-'
        }
    };

    const ipv4Details = getIPv4Details(iface);
    const ipv6Details = getIPv6Details(iface);

    if (ipv4Details) {
        obj['IPv4 Addresses'] = ipv4Details;
    }

    if (ipv6Details) {
        obj['IPv6 Addresses'] = ipv6Details;
    }

    if (iface.media !== null && iface.media !== undefined) {
        obj['Media Options'] = {
            ['Status']: iface.media.status,
            ['Type']: iface.media.type,
            ['Sub Type']: iface.media.subtype,
            ['Mode']: iface.media.mode,
            ['Options']: iface.media.options ? iface.media.options?.join(', ') || '-' : '-'
        };
    }

    return obj;
}
