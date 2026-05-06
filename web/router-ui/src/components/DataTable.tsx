import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  type ColumnDef,
  type SortingState,
  useReactTable,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { cx } from "../lib/format";
import { EmptyState } from "./Section";

type ColumnMeta = {
  align?: "left" | "right" | "center";
  className?: string;
  width?: string;
};

export type DashboardColumn<T> = {
  id: string;
  header: string;
  cell: (row: T) => ReactNode;
  sortValue?: (row: T) => string | number | boolean | null | undefined;
  align?: ColumnMeta["align"];
  className?: string;
  width?: string;
};

type DataTableProps<T> = {
  rows: T[];
  columns: DashboardColumn<T>[];
  empty?: ReactNode;
  getRowId?: (row: T, index: number) => string;
  onRowClick?: (row: T) => void;
  compact?: boolean;
};

export function DataTable<T>({ rows, columns, empty, getRowId, onRowClick, compact }: DataTableProps<T>) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const columnDefs = useMemo<Array<ColumnDef<T>>>(() => {
    return columns.map((column) => ({
      id: column.id,
      header: column.header,
      accessorFn: (row) => column.sortValue?.(row) ?? "",
      cell: (context) => column.cell(context.row.original),
      meta: {
        align: column.align,
        className: column.className,
        width: column.width,
      } satisfies ColumnMeta,
    }));
  }, [columns]);
  const table = useReactTable({
    data: rows,
    columns: columnDefs,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getRowId: getRowId ? (row, index) => getRowId(row, index) : undefined,
  });

  if (rows.length === 0) {
    return <EmptyState>{empty}</EmptyState>;
  }

  return (
    <div className={cx("table-frame", compact && "table-compact")}>
      <table>
        <thead>
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {headerGroup.headers.map((header) => {
                const meta = header.column.columnDef.meta as ColumnMeta | undefined;
                const sorted = header.column.getIsSorted();
                const SortIcon = sorted === "asc" ? ArrowUp : sorted === "desc" ? ArrowDown : ChevronsUpDown;
                return (
                  <th
                    key={header.id}
                    className={cx(meta?.align && `align-${meta.align}`, meta?.className)}
                    style={{ width: meta?.width }}
                  >
                    <button
                      type="button"
                      className="th-sort"
                      onClick={header.column.getToggleSortingHandler()}
                      aria-label={`Sort by ${String(header.column.columnDef.header)}`}
                    >
                      <span>{flexRender(header.column.columnDef.header, header.getContext())}</span>
                      <SortIcon aria-hidden="true" size={13} />
                    </button>
                  </th>
                );
              })}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr key={row.id} onClick={onRowClick ? () => onRowClick(row.original) : undefined} className={onRowClick ? "click-row" : undefined}>
              {row.getVisibleCells().map((cell) => {
                const meta = cell.column.columnDef.meta as ColumnMeta | undefined;
                return (
                  <td key={cell.id} className={cx(meta?.align && `align-${meta.align}`, meta?.className)}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
