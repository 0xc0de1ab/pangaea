import type { CSSProperties, ReactNode } from "react";
import { useMemo, useRef, useState } from "react";
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
  headerDescription?: string;
  headerExtra?: ReactNode;
  headerAction?: {
    disabled?: boolean;
    longPressMs?: number;
    onLongPress: () => void;
    pressed?: boolean;
    title?: string;
  };
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
  pagination?: {
    pageIndex: number;
    pageSize: number;
  };
};

export function DataTable<T>({ rows, columns, empty, getRowId, onRowClick, compact, pagination }: DataTableProps<T>) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const columnDefs = useMemo<Array<ColumnDef<T>>>(() => {
    return columns.map((column) => ({
      id: column.id,
      header: column.header,
      accessorFn: (row) => column.sortValue?.(row) ?? "",
      enableSorting: Boolean(column.sortValue),
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
  const sortedRows = table.getRowModel().rows;
  const pageStart = pagination ? Math.max(0, pagination.pageIndex) * Math.max(1, pagination.pageSize) : 0;
  const visibleRows = pagination ? sortedRows.slice(pageStart, pageStart + Math.max(1, pagination.pageSize)) : sortedRows;
  const rowSetKey = visibleRows.map((row) => row.id).join("|");

  return (
    <div className={cx("table-frame", compact && "table-compact")}>
      <table>
        <thead>
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {headerGroup.headers.map((header) => {
                const meta = header.column.columnDef.meta as ColumnMeta | undefined;
                const sourceColumn = columns.find((column) => column.id === header.column.id);
                const sorted = header.column.getIsSorted();
                const SortIcon = sorted === "asc" ? ArrowUp : sorted === "desc" ? ArrowDown : ChevronsUpDown;
                const canSort = header.column.getCanSort();
                return (
                  <th
                    key={header.id}
                    className={cx(meta?.align && `align-${meta.align}`, meta?.className)}
                    style={{ width: meta?.width }}
                    title={sourceColumn?.headerDescription}
                  >
                    {sourceColumn?.headerAction ? (
                      <LongPressHeaderButton
                        action={sourceColumn.headerAction}
                        label={flexRender(header.column.columnDef.header, header.getContext())}
                        description={sourceColumn.headerDescription}
                      />
                    ) : canSort ? (
                      <button
                        type="button"
                        className="th-sort"
                        onClick={header.column.getToggleSortingHandler()}
                        title={sourceColumn?.headerDescription}
                        aria-label={headerAriaLabel(String(header.column.columnDef.header), sourceColumn?.headerDescription, true)}
                      >
                        <span>{flexRender(header.column.columnDef.header, header.getContext())}</span>
                        <SortIcon aria-hidden="true" size={13} />
                      </button>
                    ) : (
                      <div className="th-static" title={sourceColumn?.headerDescription} aria-label={headerAriaLabel(String(header.column.columnDef.header), sourceColumn?.headerDescription, false)}>
                        <span>{flexRender(header.column.columnDef.header, header.getContext())}</span>
                      </div>
                    )}
                    {sourceColumn?.headerExtra ? (
                      <div className="th-extra">
                        {sourceColumn.headerExtra}
                      </div>
                    ) : null}
                  </th>
                );
              })}
            </tr>
          ))}
        </thead>
        <tbody key={rowSetKey}>
          {visibleRows.length === 0 ? (
            <tr className="table-row">
              <td colSpan={columns.length}>
                <EmptyState className="table-empty-transition">{empty}</EmptyState>
              </td>
            </tr>
          ) : (
            visibleRows.map((row, index) => (
              <tr
                key={row.id}
                onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                className={cx("table-row", onRowClick && "click-row")}
                style={{ "--row-delay": `${Math.min(index, 10) * 14}ms` } as CSSProperties}
              >
                {row.getVisibleCells().map((cell) => {
                  const meta = cell.column.columnDef.meta as ColumnMeta | undefined;
                  return (
                    <td key={cell.id} className={cx(meta?.align && `align-${meta.align}`, meta?.className)}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  );
                })}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

function headerAriaLabel(header: string, description: string | undefined, sortable: boolean) {
  const detail = description ? `${header}: ${description}` : header;
  return sortable ? `Sort by ${detail}` : detail;
}

function LongPressHeaderButton({ action, label, description }: { action: NonNullable<DashboardColumn<unknown>["headerAction"]>; label: ReactNode; description?: string }) {
  const timerRef = useRef<number | null>(null);
  const firedRef = useRef(false);
  const [holding, setHolding] = useState(false);

  function clearTimer() {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    setHolding(false);
  }

  function startPress() {
    if (action.disabled) {
      return;
    }
    firedRef.current = false;
    setHolding(true);
    timerRef.current = window.setTimeout(() => {
      firedRef.current = true;
      timerRef.current = null;
      setHolding(false);
      action.onLongPress();
    }, action.longPressMs ?? 620);
  }

  return (
    <button
      type="button"
      className={cx("th-sort", "th-long-press", holding && "holding", action.pressed && "selected")}
      disabled={action.disabled}
      title={action.title || description}
      aria-label={description ? `${String(label)}: ${description}` : undefined}
      aria-pressed={action.pressed}
      onPointerDown={startPress}
      onPointerUp={clearTimer}
      onPointerCancel={clearTimer}
      onPointerLeave={clearTimer}
      onContextMenu={(event) => event.preventDefault()}
      onClick={(event) => {
        event.preventDefault();
        firedRef.current = false;
      }}
    >
      <span>{label}</span>
    </button>
  );
}
