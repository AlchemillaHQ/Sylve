import type { Column, Row } from '$lib/types/components/tree-table';
import type { NetworkObject } from '$lib/types/network/object';
import type { SwitchList } from '$lib/types/network/switch';
import type { CellComponent } from 'tabulator-tables';
import { renderWithIcon } from '../../table';

export function generateTableData(switches: SwitchList | undefined): {
    rows: Row[];
    columns: Column[];
} {
    const columns: Column[] = [
        {
            field: 'id',
            visible: false,
            title: 'ID'
        },
        {
            field: 'name',
            title: 'Name',
            formatter(cell: CellComponent) {
                const value = cell.getValue();
                const row = cell.getRow();
                const data = row.getData();
                const pSw = data.private || false;

                if (pSw) {
                    return renderWithIcon('material-symbols-light--private-connectivity-outline', value);
                }

                return renderWithIcon('mdi:public', value);
            }
        },
        {
            field: 'ports',
            title: 'Ports',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                if (value && Array.isArray(value) && value.length > 0) {
                    return value.map((port) => `<span>${port.name}</span>`).join(', ');
                }

                return '-';
            }
        },
        {
            field: 'mtu',
            title: 'MTU'
        },
        {
            field: 'vlan',
            title: 'VLAN'
        },
        {
            field: 'ipv4',
            title: 'IPv4',
            formatter: (cell: CellComponent) => {
                const row = cell.getRow();
                const data = row.getData();

                if (data.dhcp) {
                    return 'DHCP';
                }

                let v4 = '';
                let gw4 = '';

                const networkObj = data.networkObj as NetworkObject;
                if (data.networkObj) {
                    if (networkObj && networkObj.entries) {
                        v4 = networkObj.entries[0].value || '-';
                    } else {
                        v4 = '-';
                    }
                } else {
                    v4 = '-';
                }

                const gatewayObj = data.gatewayAddressObj as NetworkObject;
                if (data.gatewayAddressObj) {
                    if (gatewayObj && gatewayObj.entries) {
                        gw4 = gatewayObj.entries[0].value || '-';
                    } else {
                        gw4 = '-';
                    }
                } else {
                    gw4 = '-';
                }

                if (v4 !== '-' && gw4 !== '-') {
                    return `<span>${v4}</span><br/><span>${gw4}</span>`;
                } else {
                    return '-';
                }
            }
        },
        {
            field: 'ipv6',
            title: 'IPv6',
            formatter: (cell: CellComponent) => {
                const row = cell.getRow();
                const data = row.getData();
                const value = cell.getValue();

                if (value === '-' && data.slaac) {
                    return 'SLAAC';
                }

                let v6 = '';
                let gw6 = '';

                const networkObj = data.network6Obj as NetworkObject;
                if (data.network6Obj) {
                    if (networkObj && networkObj.entries) {
                        v6 = networkObj.entries[0].value || '-';
                    } else {
                        v6 = '-';
                    }
                } else {
                    v6 = '-';
                }

                const gatewayObj = data.gateway6AddressObj as NetworkObject;
                if (data.gateway6AddressObj) {
                    if (gatewayObj && gatewayObj.entries) {
                        gw6 = gatewayObj.entries[0].value || '-';
                    } else {
                        gw6 = '-';
                    }
                } else {
                    gw6 = '-';
                }

                if (v6 !== '-' && gw6 !== '-') {
                    return `<span>${v6}</span><br/><span>${gw6}</span>`;
                } else {
                    return '-';
                }
            }
        },
        {
            field: 'private',
            title: 'Private',
            visible: false
        },
        {
            field: 'dhcp',
            title: 'DHCP',
            visible: false
        },
        {
            field: 'disableIPv6',
            title: 'Disable IPv6',
            visible: false
        },
        {
            field: 'slaac',
            title: 'SLAAC',
            visible: false
        },
        {
            field: 'defaultRoute',
            title: 'Default Route',
            visible: false,
            formatter: (cell: CellComponent) => {
                const row = cell.getRow();
                const data = row.getData();

                if (data.defaultRoute) {
                    return renderWithIcon('lets-icons:check-fill', 'Yes');
                }

                return renderWithIcon('gridicons:cross-circle', 'No');
            }
        }
    ];

    const rows: Row[] = [];

    if (switches && switches['standard']) {
        for (const sw of switches['standard']) {
            const portsOnly =
                sw.ports?.map((port) => {
                    return port.name;
                }) || [];

            rows.push({
                id: sw.id,
                name: sw.name,
                mtu: sw.mtu,
                vlan: sw.vlan || '-',
                ipv4: sw.address || '-',
                ipv6: sw.address6 || '-',
                addressObj: sw.addressObj || '-',
                address6Obj: sw.address6Obj || '-',
                networkObj: sw.networkObj || '-',
                gatewayAddressObj: sw.gatewayAddressObj || '-',
                network6Obj: sw.network6Obj || '-',
                gateway6AddressObj: sw.gateway6AddressObj || '-',
                ports: sw.ports,
                private: sw.private,
                portsOnly: portsOnly,
                dhcp: sw.dhcp || false,
                disableIPv6: sw.disableIPv6 || false,
                slaac: sw.slaac || false,
                defaultRoute: sw.defaultRoute || false
            });
        }
    }

    return {
        rows: rows,
        columns: columns
    };
}
