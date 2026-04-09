import type { CellComponent, EmptyCallback, FormatterParams, RowComponent } from 'tabulator-tables';

export interface Row {
    id: number | string;
    [key: string]: any;
    children?: Row[];
}

export interface Column {
    field: string;
    title: string;
    visible?: boolean;
    width?: number | string;
    copyValue?: (cell: CellComponent) => string;
    copyOnClick?: boolean | ((row: RowComponent) => boolean);
    cellClick?: (e: UIEvent, cell: CellComponent) => void;
    formatter?:
    | ((cell: CellComponent, formatterParams: FormatterParams, onRendered: EmptyCallback) => void)
    | string;
    minWidth?: number | string;
    headerFilter?: boolean | string;
    headerFilterPlaceholder?: string;
}

export interface TreeTableState {
    columnWidths: Record<string, number>;
    expandedRows: Record<string | number, boolean>;
    hiddenColumns: Record<string, boolean>;
}

export type ExpandedRows = Record<number, boolean>;
