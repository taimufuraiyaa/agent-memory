import { Alert, Button, Group, Modal, Select, Stack, Textarea } from '@mantine/core'
import { useEffect, useState } from 'react'
import type { GraphReviewInput } from '../../lib/knowledgeGateway'

const actions: Array<{ value: GraphReviewInput['action']; label: string }> = [
  { value: 'approve', label: 'Approve' }, { value: 'reject', label: 'Reject' },
  { value: 'supersede', label: 'Supersede' }, { value: 'annotate', label: 'Annotate' },
  { value: 'reconsider', label: 'Reconsider' },
]

function targetTrust(action: GraphReviewInput['action'], current: string): string {
  if (action === 'approve') return 'approved'
  if (action === 'reject') return 'rejected'
  if (action === 'supersede') return 'superseded'
  if (action === 'reconsider') return 'reviewed'
  return current
}

export function GraphReview({ opened, target, onClose, onSubmit }: {
  opened: boolean
  target: { kind: GraphReviewInput['targetKind']; id: string; trust: string; version: number; label: string } | null
  onClose: () => void
  onSubmit: (input: GraphReviewInput) => Promise<void>
}) {
  const [action, setAction] = useState<GraphReviewInput['action']>('approve')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { if (opened) { setAction(target?.trust === 'rejected' || target?.trust === 'stale' ? 'reconsider' : 'approve'); setReason(''); setError('') } }, [opened, target])
  async function submit() {
    if (!target || busy) return
    setBusy(true); setError('')
    try {
      await onSubmit({ targetKind: target.kind, targetId: target.id, action, from: target.trust, to: targetTrust(action, target.trust), expectedVersion: target.version, reason: reason.trim() })
      onClose()
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Review could not be recorded.') } finally { setBusy(false) }
  }
  return <Modal opened={opened} onClose={onClose} title={`Review ${target?.label || 'graph record'}`} size="md" closeButtonProps={{ 'aria-label': 'Close graph review' }}>
    <Stack>
      <Select label="Review decision" value={action} data={actions} onChange={(value) => setAction((value || 'approve') as GraphReviewInput['action'])} />
      <Textarea label="Reason or annotation" description="Stored with the review; do not include secrets." value={reason} onChange={(event) => setReason(event.currentTarget.value)} minRows={3} maxLength={2000} />
      {error ? <Alert color="red" title="Review failed" role="alert">{error}</Alert> : null}
      <Group justify="flex-end"><Button variant="default" onClick={onClose}>Cancel</Button><Button loading={busy} onClick={() => void submit()}>Record review</Button></Group>
    </Stack>
  </Modal>
}
