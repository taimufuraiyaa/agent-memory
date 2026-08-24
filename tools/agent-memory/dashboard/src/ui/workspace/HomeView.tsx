import { Button, Card, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { IconActivity, IconBooks, IconBrain, IconMessageCircleQuestion, IconPlus, IconSearch, IconSparkles } from '@tabler/icons-react'
import type { WorkspaceSummary } from '../../lib/knowledgeGateway'

export function HomeView({ workspace, onNavigate, onAddSource }: {
  workspace: WorkspaceSummary
  onNavigate: (target: 'ask' | 'sources' | 'search' | 'browse' | 'activity') => void
  onAddSource: () => void
}) {
  return <Stack className="homeView" gap="lg">
    <Paper className="homeHero" withBorder p={{ base: 'xl', md: 48 }} radius="xl">
      <Stack gap="lg" maw={900}>
        <Group gap="xs"><ThemeIcon variant="light" color="memory"><IconSparkles size={18} /></ThemeIcon><Text c="memory" size="xs" fw={700} tt="uppercase">{workspace.kind === 'registered-project' ? 'Registered project' : 'Private knowledge workspace'}</Text></Group>
        <div><Title order={2}>What do you want to know about {workspace.name}?</Title><Text c="dimmed" size="lg" mt="sm">Import or study material once, then ask it, search exact memories, or browse what was retained.</Text></div>
        <Group><Button size="md" leftSection={<IconMessageCircleQuestion size={18} />} onClick={() => onNavigate('ask')}>Ask this project</Button><Button size="md" variant="light" leftSection={<IconSearch size={18} />} onClick={() => onNavigate('search')}>Search extracted memories</Button><Button size="md" variant="default" leftSection={<IconBrain size={18} />} onClick={() => onNavigate('browse')}>Browse memories</Button></Group>
      </Stack>
    </Paper>
    <SimpleGrid className="homeCards" cols={{ base: 1, md: 3 }}>
      <Card withBorder radius="lg" padding="lg"><Stack h="100%"><ThemeIcon variant="light" size="lg"><IconBooks size={20} /></ThemeIcon><Text size="xl" fw={750}>{workspace.sourceCount}</Text><Title order={3}>Sources</Title><Text c="dimmed">Codebases, books, documents, and notes in this scope.</Text><Group mt="auto"><Button size="xs" leftSection={<IconPlus size={15} />} onClick={onAddSource}>Add source</Button><Button size="xs" variant="subtle" onClick={() => onNavigate('sources')}>Open Sources</Button></Group></Stack></Card>
      <Card withBorder radius="lg" padding="lg"><Stack h="100%"><ThemeIcon variant="light" size="lg"><IconBrain size={20} /></ThemeIcon><Text size="xl" fw={750}>{workspace.memoryCount}</Text><Title order={3}>Memories</Title><Text c="dimmed">Durable knowledge available to agents and workspace questions.</Text><Button mt="auto" size="xs" variant="light" onClick={() => onNavigate('browse')}>Browse memories</Button></Stack></Card>
      <Card withBorder radius="lg" padding="lg"><Stack h="100%"><ThemeIcon variant="light" size="lg"><IconActivity size={20} /></ThemeIcon><Text size="xl" fw={750}>{workspace.noteCount}</Text><Title order={3}>Recent work</Title><Text c="dimmed">Study, upload, indexing, retrieval, and feedback status.</Text><Button mt="auto" size="xs" variant="light" onClick={() => onNavigate('activity')}>View Activity</Button></Stack></Card>
    </SimpleGrid>
  </Stack>
}
