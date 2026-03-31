import type { Column, Row } from '$lib/types/components/tree-table';
import type { Download } from '$lib/types/utilities/downloader';
import type { VM } from '$lib/types/vm/vm';
import type { Dataset } from '$lib/types/zfs/dataset';
import type { CellComponent } from 'tabulator-tables';
import { formatBytesBinary } from '../bytes';
import { renderWithIcon } from '../table';

export function generateTableData(
    vm: VM,
    datasets: Dataset[],
    downloads: Download[]
): {
    rows: Row[];
    columns: Column[];
} {
    const rows: Row[] = [];
    const columns: Column[] = [
        {
            field: 'id',
            title: 'ID',
            visible: false
        },
        {
            field: 'enabled',
            title: 'Status',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                return value ? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500') : renderWithIcon('mdi:close-circle', 'Disabled', 'text-red-500');
            }
        },
        {
            field: 'type',
            title: 'Type',
            visible: false
        },
        {
            field: 'name',
            title: 'Name',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                const row = cell.getRow().getData();

                if (row.type === 'image') {
                    return renderWithIcon('tdesign:cd-filled', value, 'text-green-500', 'Installation Media');
                } else if (row.type === 'zvol') {
                    return renderWithIcon(
                        'carbon:volume-block-storage',
                        value,
                        'text-blue-500',
                        'ZFS Volume'
                    );
                } else if (row.type === 'raw') {
                    return renderWithIcon('carbon:volume-block-storage', value, 'text-blue-500', 'Raw Disk');
                } else if (row.type === 'filesystem') {
                    return renderWithIcon('mdi:folder-network', value, 'text-amber-500', '9P Filesystem');
                }
                return value;
            }
        },
        {
            field: 'emulation',
            title: 'Emulation',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                switch (value) {
                    case 'ahci-cd':
                        return 'AHCI CD-ROM';
                    case 'virtio-blk':
                        return 'VirtIO Block';
                    case 'ahci-hd':
                        return 'AHCI Hard Disk';
                    case 'nvme':
                        return 'NVMe';
                    case 'virtio-9p':
                        return 'VirtIO 9P';
                    default:
                        break;
                }
                return '-';
            }
        },
        {
            field: 'bootorder',
            title: 'Boot Order',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                return value !== undefined ? value : '-';
            }
        },
        {
            field: 'size',
            title: 'Size',
            formatter: (cell: CellComponent) => {
                const value = cell.getValue();
                if (value === 0) {
                    return '-';
                }

                return formatBytesBinary(value);
            }
        }
    ];

    const storages = vm.storages || [];

    let zvolCount = 0;
    let rawCount = 0;

    for (const storage of storages) {
        let name = '';
        let size = 0;

        if (storage.type === 'image') {
            const download = downloads.find((d) => storage.uuid === d.uuid);
            name = download ? download.name : 'Unknown ISO';
            size = download ? download.size : 0;

            name = `${storage.name} (${name})`;
        } else if (storage.type === 'zvol' || storage.type === 'raw') {
            if (storage.type === 'zvol') {
                zvolCount++;
                name = storage.name ? storage.name : `ZFS Volume - ${zvolCount}`;
            } else if (storage.type === 'raw') {
                rawCount++;
                name = storage.name ? storage.name : `Raw Disk - ${rawCount}`;
            }
        } else if (storage.type === 'filesystem') {
            const datasetName = storage.dataset?.name || 'Unknown dataset';
            const target = storage.filesystemTarget || storage.name || `share-${storage.id}`;
            const mode = storage.readOnly ? 'ro' : 'rw';
            name = `${target} (${datasetName}, ${mode})`;
            size = 0;
        }

        rows.push({
            id: storage.id,
            enabled: storage.enable,
            type: storage.type,
            emulation: storage.emulation,
            bootorder: storage.type === 'filesystem' ? undefined : (storage.bootOrder ?? 0),
            name: name,
            size: size || storage.size
        });
    }

    return {
        rows: rows,
        columns
    };
}
