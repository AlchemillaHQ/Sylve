import type { Column, Row } from '$lib/types/components/tree-table';
import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
import type { CellComponent } from 'tabulator-tables';
import { generateNumberFromString } from '../numbers';
import { renderWithIcon } from '../table';

export function findPPTDevice(device: PCIDevice, pptDevices: PPTDevice[]): PPTDevice | undefined {
	const id = `${device.bus}/${device.device}/${device['function']}`;
	return pptDevices.find((ppt) => ppt.domain === device.domain && ppt.deviceID === id);
}

function getPassthroughStatus(device: PCIDevice, pptDevices: PPTDevice[]): string {
	const mappedDevice = findPPTDevice(device, pptDevices);
	if (device.domain !== 0 && !mappedDevice) return 'unsupported-domain';
	if (mappedDevice && !device.name.startsWith('ppt')) return 'managed-not-attached';

	if (device.name.startsWith('ppt')) {
		if (mappedDevice) {
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
				} else if (status === 'unsupported-domain') {
					return renderWithIcon(
						'wpf:connected',
						device,
						'text-orange-500',
						'Only PCI domain 0 is supported for new passthrough mappings'
					);
				} else if (status === 'managed-not-attached') {
					return renderWithIcon(
						'wpf:connected',
						device,
						'text-orange-500',
						'This managed mapping is not currently attached to ppt. It can be disabled or may require a reboot.'
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
				device.domain +
				device.bus +
				device.unit +
				(device.class || '') +
				(device.device || '') +
				(device['function'] || '') +
				(device.vendor || '')
		);

		const deviceId = `${device.bus}/${device.device}/${device['function']}`;

		const pptDevice = findPPTDevice(device, pptDevices);
		const pptId = pptDevice ? pptDevice.id.toString() : '';

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
	const pptDevice = findPPTDevice(device, pptDevices);
	return pptDevice?.id || 0;
}
