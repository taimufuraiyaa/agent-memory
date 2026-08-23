import { ActionIcon, Badge, Group, Paper, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { IconPin, IconPinnedOff, IconPrinter, IconTrash } from '@tabler/icons-react'
import type { KnowledgeResult } from '../../lib/knowledgeGateway'

export function KnowledgeResultCard({ result, selected, onOpen, onTogglePin, onDelete }: {
  result: KnowledgeResult
  selected?: boolean
  onOpen?: () => void
  onTogglePin?: () => void
  onDelete?: () => void
}) {
  return <Paper component="article" className={selected ? 'knowledgeResult isSelected' : 'knowledgeResult'} withBorder radius="lg">
    <UnstyledButton className="knowledgeResultBody" onClick={onOpen}>
      <Stack gap="xs">
        <Badge variant="light" color={result.kind === 'source-evidence' ? 'blue' : 'memory'}>{result.kind === 'source-evidence' ? 'Source evidence' : result.memoryType || 'Memory'}</Badge>
        {result.title ? <Text fw={700}>{result.title}</Text> : null}
        <Text lh={1.6}>{result.content}</Text>
        <Group gap="md" c="dimmed">
          {result.provenance ? <Text size="xs">{result.provenance}</Text> : null}
          {typeof result.relevance === 'number' ? <Text size="xs">{Math.round(result.relevance * 100)}% relevance</Text> : null}
          {result.updatedAt ? <Text component="time" size="xs" dateTime={result.updatedAt}>{new Date(result.updatedAt).toLocaleString()}</Text> : null}
        </Group>
        {result.explanation ? <Text size="xs" c="dimmed">Why this matched: {result.explanation}</Text> : null}
      </Stack>
    </UnstyledButton>
    {result.kind === 'memory' ? <Group className="knowledgeResultActions" justify="flex-end" gap="xs">
      {result.actions.includes(result.pinned ? 'unpin' : 'pin') && onTogglePin ? <Tooltip label={result.pinned ? 'Unpin memory' : 'Pin memory'}><ActionIcon variant="subtle" aria-label={result.pinned ? 'Unpin memory' : 'Pin memory'} onClick={onTogglePin}>{result.pinned ? <IconPinnedOff size={17} /> : <IconPin size={17} />}</ActionIcon></Tooltip> : null}
      {result.actions.includes('print') ? <Tooltip label="Print memory"><ActionIcon variant="subtle" aria-label="Print memory" onClick={() => window.print()}><IconPrinter size={17} /></ActionIcon></Tooltip> : null}
      {result.actions.includes('delete') && onDelete ? <Tooltip label="Delete memory"><ActionIcon color="red" variant="subtle" aria-label="Delete memory" onClick={onDelete}><IconTrash size={17} /></ActionIcon></Tooltip> : null}
    </Group> : null}
  </Paper>
}
