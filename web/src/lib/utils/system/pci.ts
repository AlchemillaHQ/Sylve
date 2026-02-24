import type { Column, Row } from '$lib/types/components/tree-table';
import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
import type { CellComponent } from 'tabulator-tables';
import { generateNumberFromString } from '../numbers';
import { renderWithIcon } from '../table';

function getPassthroughStatus(device: PCIDevice, pptDevices: PPTDevice[]): string {
    if (device.name.startsWith('ppt')) {
        const id = `${device.bus}/${device.device}/${device['function']}`;
        if (pptDevices.some((ppt) => ppt.deviceID === id)) {
            return 'passed-through-in-db';
        } else {
            return 'passed-through-not-in-db';
        }
    }

    return 'not-passed-through';
}

export function generateTableData(
    pciDevices: PCIDevice[],
    pptDevices: PPTDevice[]
): {
    rows: Row[];
    columns: Column[];
} {
    const rows: Row[] = [];
    const columns: Column[] = [
        {
            field: 'deviceId',
            title: 'Device ID',
            visible: true,
            width: '15%'
        },
        {
            field: 'status',
            title: 'Status',
            visible: false
        },
        {
            field: 'id',
            title: 'ID',
            visible: false
        },
        {
            field: 'name',
            title: 'Name',
            visible: false
        },
        {
            field: 'device',
            title: 'Device',
            visible: true,
            formatter: (cell: CellComponent) => {
                const data = cell.getData();
                const device = data.device || '-';
                const status = data.status || 'not-passed-through';
                if (status === 'not-passed-through') {
                    return renderWithIcon(
                        'wpf:connected',
                        device,
                        'text-green-500',
                        'This device is connected to the host'
                    );
                } else if (status === 'passed-through-in-db') {
                    return renderWithIcon(
                        'wpf:connected',
                        device,
                        'text-blue-500',
                        'This device is ready for passthrough'
                    );
                } else if (status === 'passed-through-not-in-db') {
                    return renderWithIcon(
                        'wpf:connected',
                        device,
                        'text-yellow-500',
                        'This device is on ppt but not managed by Sylve yet. Import it to manage from here.'
                    );
                }

                return device;
            }
        },
        {
            field: 'vendor',
            title: 'Vendor',
            visible: true
        },
        {
            field: 'class',
            title: 'Class',
            visible: true
        },
        {
            field: 'subclass',
            title: 'Subclass',
            visible: true
        },
        {
            field: 'domain',
            title: 'Domain',
            visible: false
        },
        {
            field: 'pptId',
            title: 'PPT ID',
            visible: false
        }
    ];

    for (const device of pciDevices) {
        const id = generateNumberFromString(
            device.name +
            device.bus +
            (device.class || '') +
            (device.device || '') +
            (device['function'] || '') +
            (device.vendor || '')
        );

        const deviceId = `${device.bus}/${device.device}/${device['function']}`;

        let pptId = '';
        if (device.name.startsWith('ppt')) {
            const pptDevice = pptDevices.find((ppt) => ppt.deviceID === deviceId);
            pptId = pptDevice ? pptDevice.id.toString() : '';
        }

        rows.push({
            status: getPassthroughStatus(device, pptDevices),
            id: id,
            name: device.name || '-',
            device: device.names.device || '-',
            vendor: device.names.vendor || '-',
            class: device.names.class || '-',
            subclass: device.names.subclass || '-',
            domain: device.domain,
            deviceId,
            pptId: pptId
        });
    }

    return {
        rows: rows,
        columns: columns
    };
}

export function getPCIDeviceId(device: PCIDevice): string {
    return `pci${device.domain}:${device.bus}:${device.device}:${device['function']}`;
}

export function getPPTDeviceId(device: PCIDevice, pptDevices: PPTDevice[]): number {
    const id = `${device.bus}/${device.device}/${device['function']}`;
    const pptDevice = pptDevices.find((ppt) => ppt.deviceID === id);
    return pptDevice?.id || 0;
}
