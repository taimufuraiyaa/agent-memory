import { ActionIcon, Badge, Button, Group, Paper, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { IconCheck, IconCopy, IconPin, IconPinnedOff, IconPrinter, IconTrash } from '@tabler/icons-react'
import type { KnowledgeResult } from '../../lib/knowledgeGateway'

export function KnowledgeResultCard({ result, selected, previewLines, copied, onOpen, onCopy, onTogglePin, onDelete }: {
  result: KnowledgeResult
  selected?: boolean
  previewLines?: number
  copied?: boolean
  onOpen?: () => void
  onCopy?: () => void
  onTogglePin?: () => void
  onDelete?: () => void
}) {
  return <Paper component="article" className={selected ? 'knowledgeResult isSelected' : 'knowledgeResult'} withBorder radius="lg">
    <UnstyledButton className="knowledgeResultBody" data-openable={onOpen || undefined} onClick={onOpen}>
      <Stack gap="xs">
        <Badge variant="light" color={result.kind === 'source-evidence' ? 'blue' : 'memory'}>{result.kind === 'source-evidence' ? 'Source evidence' : result.memoryType || 'Memory'}</Badge>
        {result.title ? <Text fw={700}>{result.title}</Text> : null}
        <Text lh={1.6} lineClamp={previewLines}>{result.content}</Text>
        <Group gap="md" c="dimmed">
          {result.provenance ? <Text size="xs">{result.provenance}</Text> : null}
          {typeof result.relevance === 'number' ? <Text size="xs">{Math.round(result.relevance * 100)}% relevance</Text> : null}
          {result.updatedAt ? <Text component="time" size="xs" dateTime={result.updatedAt}>{new Date(result.updatedAt).toLocaleString()}</Text> : null}
        </Group>
        {result.explanation ? <Text size="xs" c="dimmed">Why this matched: {result.explanation}</Text> : null}
      </Stack>
    </UnstyledButton>
    {result.kind === 'source-evidence' && onCopy ? <Group className="knowledgeResultActions" justify="flex-end" gap="xs">
      <Button variant="subtle" size="compact-sm" leftSection={copied ? <IconCheck size={15} /> : <IconCopy size={15} />} onClick={onCopy} aria-label="Copy evidence">{copied ? 'Copied' : 'Copy'}</Button>
    </Group> : null}
    {result.kind === 'memory' ? <Group className="knowledgeResultActions" justify="flex-end" gap="xs">
      {result.actions.includes(result.pinned ? 'unpin' : 'pin') && onTogglePin ? <Tooltip label={result.pinned ? 'Unpin memory' : 'Pin memory'}><ActionIcon variant="subtle" aria-label={result.pinned ? 'Unpin memory' : 'Pin memory'} onClick={onTogglePin}>{result.pinned ? <IconPinnedOff size={17} /> : <IconPin size={17} />}</ActionIcon></Tooltip> : null}
      {result.actions.includes('print') ? <Tooltip label="Print memory"><ActionIcon variant="subtle" aria-label="Print memory" onClick={() => window.print()}><IconPrinter size={17} /></ActionIcon></Tooltip> : null}
      {result.actions.includes('delete') && onDelete ? <Tooltip label="Delete memory"><ActionIcon color="red" variant="subtle" aria-label="Delete memory" onClick={onDelete}><IconTrash size={17} /></ActionIcon></Tooltip> : null}
    </Group> : null}
  </Paper>
}
