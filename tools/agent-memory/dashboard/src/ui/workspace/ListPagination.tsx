import { Button, Group, Pagination, Text } from '@mantine/core'

export const LIST_PAGE_SIZE = 10

export function paginateRecords<T>(records: T[], requestedPage: number, pageSize = LIST_PAGE_SIZE) {
  const totalPages = Math.max(1, Math.ceil(records.length / pageSize))
  const page = Math.min(Math.max(1, requestedPage), totalPages)
  const startIndex = (page - 1) * pageSize
  return { page, totalPages, items: records.slice(startIndex, startIndex + pageSize), start: records.length ? startIndex + 1 : 0, end: Math.min(startIndex + pageSize, records.length) }
}

export function ListPagination({ page, total, onChange, label, pageSize = LIST_PAGE_SIZE }: { page: number; total: number; onChange: (page: number) => void; label: string; pageSize?: number }) {
  const result = paginateRecords(Array.from({ length: total }), page, pageSize)
  if (total <= pageSize) return null
  return <Group className="recordPagination" component="nav" aria-label={`${label} pagination`} justify="space-between" gap="sm"><Text size="sm" c="dimmed">Showing {result.start}–{result.end} of {total} records</Text><Pagination value={result.page} total={result.totalPages} onChange={onChange} size="sm" withEdges /></Group>
}

export function CursorPagination({ page, hasNext, busy, onPrevious, onNext, label }: { page: number; hasNext: boolean; busy: boolean; onPrevious: () => void; onNext: () => void; label: string }) {
  if (page === 1 && !hasNext) return null
  return <Group className="recordPagination" component="nav" aria-label={`${label} pagination`} justify="space-between" gap="sm"><Text size="sm" c="dimmed">Page {page}</Text><Group gap="xs"><Button variant="default" size="sm" disabled={page <= 1 || busy} onClick={onPrevious}>Previous</Button><Button variant="light" size="sm" disabled={!hasNext || busy} onClick={onNext}>Next</Button></Group></Group>
}
