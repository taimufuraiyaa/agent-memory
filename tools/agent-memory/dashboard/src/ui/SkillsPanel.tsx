import { useEffect, useMemo, useState } from 'react'
import { Alert, Badge, Button, Group, Paper, Stack, Table, Text, Title } from '@mantine/core'
import type { SkillInfo, SkillLifecycleDetail, SkillLifecycleSummary, SkillOrchestrationControl, SkillOrchestrationJob, SkillOrchestrationStatus } from '../lib/api'
import { MarkdownView } from './MarkdownView'
import { ListPagination, paginateRecords } from './workspace/ListPagination'

type Props = {
  theme: 'light' | 'dark'
  workspace: string
  skills: SkillInfo[]
  lifecycleSkills: SkillLifecycleSummary[]
  busy: boolean
  error: string
  inspect: (skillId: string) => Promise<SkillLifecycleDetail>
  approve: (detail: SkillLifecycleDetail, revisionId: string) => Promise<void>
  rollback: (detail: SkillLifecycleDetail) => Promise<void>
  inspectOrchestration: (skillId: string, signal?: AbortSignal) => Promise<SkillOrchestrationStatus>
  controlOrchestration: (input: SkillOrchestrationControl) => Promise<void>
}

const short = (value?: string) => value ? `${value.slice(0, 12)}${value.length > 12 ? '…' : ''}` : 'N/A'

export function SkillsPanel({ theme, workspace, skills, lifecycleSkills, busy, error, inspect, approve, rollback, inspectOrchestration, controlOrchestration }: Props) {
  const [selectedId, setSelectedId] = useState('')
  const [detail, setDetail] = useState<SkillLifecycleDetail | null>(null)
  const [actionError, setActionError] = useState('')
  const [acting, setActing] = useState(false)
  const [orchestration, setOrchestration] = useState<SkillOrchestrationStatus | null>(null)
  const [orchestrationError, setOrchestrationError] = useState('')
  const [skillPage, setSkillPage] = useState(1)
  const pagedSkills = paginateRecords(lifecycleSkills, skillPage)
  const latest = detail?.revisions[0]
  const legacy = useMemo(() => skills.find((item) => item.name === detail?.skill.name), [detail?.skill.name, skills])
  const evaluations = useMemo(() => new Map((detail?.evaluations || []).map((item) => [item.revision_id, item])), [detail?.evaluations])
  const decisions = useMemo(() => new Map((detail?.policy_decisions || []).map((item) => [item.revision_id, item])), [detail?.policy_decisions])

  useEffect(() => {
    setSelectedId((current) => lifecycleSkills.some((skill) => skill.id === current) ? current : lifecycleSkills[0]?.id || '')
  }, [lifecycleSkills])

  useEffect(() => setSkillPage(1), [workspace])

  useEffect(() => {
    let current = true
    setDetail(null)
    setActionError('')
    if (selectedId) inspect(selectedId).then((value) => { if (current) setDetail(value) }).catch((cause) => { if (current) setActionError(cause instanceof Error ? cause.message : String(cause)) })
    return () => { current = false }
  }, [inspect, selectedId])

  useEffect(() => {
    if (!selectedId) { setOrchestration(null); return }
    const controller = new AbortController()
    let current = true
    const refresh = () => inspectOrchestration(selectedId, controller.signal).then((value) => {
      if (current) { setOrchestration(value); setOrchestrationError('') }
    }).catch((cause) => {
      if (current && !controller.signal.aborted) { setOrchestration(null); setOrchestrationError(cause instanceof Error ? cause.message : String(cause)) }
    })
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5_000)
    return () => { current = false; controller.abort(); window.clearInterval(timer) }
  }, [inspectOrchestration, selectedId])

  async function run(action: () => Promise<void>) {
    setActing(true)
    setActionError('')
    try {
      await action()
      if (selectedId) setDetail(await inspect(selectedId))
    } catch (cause) { setActionError(cause instanceof Error ? cause.message : String(cause)) }
    finally { setActing(false) }
  }

  async function runOrchestration(input: SkillOrchestrationControl) {
    setActing(true); setOrchestrationError('')
    try {
      await controlOrchestration(input)
      if (selectedId) setOrchestration(await inspectOrchestration(selectedId))
    } catch (cause) {
      setOrchestrationError(cause instanceof Error ? cause.message : String(cause))
      if (selectedId) inspectOrchestration(selectedId).then(setOrchestration).catch(() => undefined)
    }
    finally { setActing(false) }
  }

  if (!workspace) return <Paper withBorder p="xl"><Text>Select a workspace to view its skills.</Text></Paper>

  return <Stack className="skillsPanel" gap="md">
    <div><Title order={3}>Skill revision lifecycle</Title><Text c="dimmed">The active revision runs now. The latest revision remains inactive until evaluation and policy gates pass.</Text></div>
    {error || actionError ? <Alert color="red" role="alert">{error || actionError}</Alert> : null}
    {busy && lifecycleSkills.length === 0 ? <Text role="status">Loading skill lifecycle…</Text> : null}
    {!busy && lifecycleSkills.length === 0 ? <Alert color="gray">No revision-managed skills found. Legacy files remain unchanged until imported or distilled.</Alert> : null}
    {lifecycleSkills.length > 0 ? <div className="skillsBrowser">
      <nav className="skillsDirectory" aria-label="Revision-managed skills">
        {pagedSkills.items.map((skill) => <button className="skillDirectoryItem" data-selected={selectedId === skill.id || undefined} key={skill.id} type="button" onClick={() => setSelectedId(skill.id)}>
          <strong>{skill.name}</strong><span>{skill.description || 'No description'}</span><Badge size="xs" variant="light">{skill.risk_tier} risk</Badge>
        </button>)}
        <ListPagination page={pagedSkills.page} total={lifecycleSkills.length} onChange={setSkillPage} label="Skills" />
      </nav>
      <section className="skillsDetail" aria-live="polite">
        {!detail ? <Text role="status">Loading revision details…</Text> : <Stack gap="md">
          <Group justify="space-between" align="flex-start"><div><Title order={3}>{detail.skill.name}</Title><Text c="dimmed">Owner: {detail.skill.owner_group || 'N/A'}</Text></div><Badge color={detail.skill.status === 'active' ? 'green' : 'gray'}>{detail.skill.status}</Badge></Group>
          <div className="skillStateGrid">
            <State label="Latest" value={latest ? `v${latest.number} · ${latest.state}` : 'N/A'} />
            <State label="Active" value={short(detail.activation?.active_revision_id)} />
            <State label="Canary" value={short(detail.activation?.canary_revision_id)} />
            <State label="Last known good" value={short(detail.activation?.last_known_good_revision_id)} />
          </div>
          <Group className="skillLifecycleActions">
            <Button disabled={acting || !latest || latest.id === detail.activation?.active_revision_id || !['testing', 'canary'].includes(latest.state)} onClick={() => latest && void run(() => approve(detail, latest.id))}>Approve latest</Button>
            <Button color="orange" variant="outline" disabled={acting || !detail.activation?.last_known_good_revision_id || detail.activation.last_known_good_revision_id === detail.activation.active_revision_id} onClick={() => void run(() => rollback(detail))}>Rollback to last known good</Button>
          </Group>
          <Paper withBorder p="md" className="skillOrchestration" aria-label="Automatic revision workflow">
            <Stack gap="sm">
              <Group justify="space-between"><div><Title order={4}>Automatic revision workflow</Title><Text size="sm" c="dimmed">Operational controls do not approve a revision or bypass policy gates.</Text></div>{orchestration ? <Badge variant="light">{orchestration.workflow.state}</Badge> : null}</Group>
              {orchestrationError ? <Alert color="gray" role="status">Workflow state: N/A — {orchestrationError}</Alert> : null}
              {orchestration ? <>
                <div className="skillStateGrid">
                  <State label="Stage" value={orchestration.workflow.current_stage || 'N/A'} />
                  <State label="Generation" value={String(orchestration.workflow.generation || 'N/A')} />
                  <State label="Configuration" value={String(orchestration.workflow.configuration_version || 'N/A')} />
                  <State label="Policy" value={short(orchestration.workflow.policy_digest)} />
                </div>
                <Group className="skillLifecycleActions">
                  <Button size="xs" variant="outline" disabled={acting || !['open', 'paused'].includes(orchestration.workflow.state)} onClick={() => void runOrchestration({ action: orchestration.workflow.state === 'paused' ? 'resume' : 'pause', workflow_id: orchestration.workflow.id, expected_generation: orchestration.workflow.generation })}>{orchestration.workflow.state === 'paused' ? 'Resume workflow' : 'Pause workflow'}</Button>
                  <Button size="xs" variant="outline" disabled={acting || orchestration.workflow.state !== 'open'} onClick={() => void runOrchestration({ action: 'reconcile', workflow_id: orchestration.workflow.id, expected_generation: orchestration.workflow.generation, limit: 50 })}>Reconcile blocked work</Button>
                </Group>
                <Text size="sm" fw={700}>Jobs and safe reasons</Text>
                {orchestration.jobs.length === 0 ? <Text size="sm" c="dimmed">No jobs recorded.</Text> : <Stack gap="xs">{orchestration.jobs.map((job) => <JobRow key={job.id} job={job} generation={orchestration.workflow.generation} acting={acting} control={(input) => void runOrchestration(input)} />)}</Stack>}
              </> : !orchestrationError ? <Text role="status">Loading automatic workflow…</Text> : null}
            </Stack>
          </Paper>
          <Paper withBorder p="sm" className="skillRevisionTable"><Table.ScrollContainer minWidth={720}><Table striped highlightOnHover>
            <Table.Thead><Table.Tr><Table.Th>Revision</Table.Th><Table.Th>State</Table.Th><Table.Th>Digest</Table.Th><Table.Th>Created by</Table.Th><Table.Th>Provenance</Table.Th><Table.Th>Evaluation</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>{detail.revisions.map((revision) => <Table.Tr key={revision.id} data-active={revision.id === detail.activation?.active_revision_id || undefined}>
              <Table.Td>v{revision.number}</Table.Td><Table.Td><Badge variant="light">{revision.state}</Badge></Table.Td><Table.Td><code>{short(revision.bundle_digest)}</code></Table.Td><Table.Td>{revision.created_by || 'N/A'}</Table.Td><Table.Td><details><summary>{(revision.source_memory_ids?.length || 0) + (revision.source_tool_lesson_ids?.length || 0) + (revision.source_episode_ids?.length || 0)} sources</summary><Text size="xs">Memories: {revision.source_memory_ids?.join(', ') || 'N/A'}<br />Lessons: {revision.source_tool_lesson_ids?.join(', ') || 'N/A'}<br />Episodes: {revision.source_episode_ids?.join(', ') || 'N/A'}</Text></details></Table.Td><Table.Td>{evaluations.get(revision.id)?.verdict || 'N/A'}{decisions.get(revision.id) ? <Text size="xs" c="dimmed">{decisions.get(revision.id)?.decision}: {decisions.get(revision.id)?.reason_codes.join(', ')}</Text> : null}</Table.Td>
            </Table.Tr>)}</Table.Tbody>
          </Table></Table.ScrollContainer></Paper>
          {legacy ? <details><summary>Active materialized skill file</summary><Paper withBorder p="md"><MarkdownView markdown={legacy.content} clamp={false} theme={theme} /></Paper></details> : null}
        </Stack>}
      </section>
    </div> : null}
  </Stack>
}

function JobRow({ job, generation, acting, control }: { job: SkillOrchestrationJob; generation: number; acting: boolean; control: (input: SkillOrchestrationControl) => void }) {
  const canaryDue = job.stage === 'analyze_canary' ? (new Date(job.ready_at).getTime() <= Date.now() ? 'due' : 'waiting') : 'N/A'
  const reason = job.failure_code || job.blocked_reason || job.failure_class || 'N/A'
  const retry = () => {
    if (job.state !== 'dead_lettered') { control({ action: 'retry', job_id: job.id, expected_generation: generation }); return }
    const reason = window.prompt('Replay reason code:')?.trim()
    if (reason) control({ action: 'replay', job_id: job.id, reason_code: reason, idempotency_key: crypto.randomUUID() })
  }
  return <Paper withBorder p="sm" className="skillJobRow"><Group justify="space-between" align="flex-start"><div><Group gap="xs"><Badge size="sm">{job.state}</Badge><Text fw={700}>{job.stage}</Text></Group><Text size="xs" c="dimmed">Attempt {job.attempt}/{job.max_attempts} · Policy v{job.policy_version} · Canary check: {canaryDue}</Text><Text size="xs">Reason: {reason}</Text></div><Group gap="xs"><Button size="compact-xs" variant="subtle" disabled={acting || !['queued', 'blocked', 'running', 'retry_wait'].includes(job.state)} onClick={() => control({ action: 'cancel', job_id: job.id, expected_generation: generation })}>Cancel</Button><Button size="compact-xs" variant="subtle" disabled={acting || !['retry_wait', 'dead_lettered'].includes(job.state)} onClick={retry}>Retry</Button></Group></Group></Paper>
}

function State({ label, value }: { label: string; value: string }) {
  return <Paper withBorder p="sm"><Text size="xs" c="dimmed" tt="uppercase" fw={700}>{label}</Text><Text fw={700}>{value}</Text></Paper>
}
