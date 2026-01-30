import type { CellComponent, EmptyCallback, FormatterParams } from 'tabulator-tables';

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
    copyOnClick?: boolean | ((row: Row) => boolean);
    formatter?:
    | ((cell: CellComponent, formatterParams: FormatterParams, onRendered: EmptyCallback) => void)
    | string;
}

export interface TreeTableState {
    columnWidths: Record<string, number>;
    expandedRows: Record<string | number, boolean>;
}

export type ExpandedRows = Record<number, boolean>;
