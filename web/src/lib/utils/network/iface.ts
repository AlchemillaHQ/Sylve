import type { Column, Row } from '$lib/types/components/tree-table';
import type { Iface } from '$lib/types/network/iface';
import { generateNumberFromString } from '../numbers';

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
            metric: iface.metric,
            mtu: iface.mtu,
            media: iface.media,
            isBridge: isBridge,
            isEpair: isEpair,
            isTap: isTap,
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
