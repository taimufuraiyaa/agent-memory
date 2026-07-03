import React, { useState, useEffect } from 'react'
import type { SkillInfo } from '../lib/api'
import { MarkdownView } from './MarkdownView'

export function SkillsPanel({
  theme,
  workspace,
  skills,
  busy,
  error,
}: {
  theme: 'light' | 'dark'
  workspace: string
  skills: SkillInfo[]
  busy: boolean
  error: string
}) {
  const [selectedSkill, setSelectedSkill] = useState<SkillInfo | null>(null)

  // Auto-select first skill when skills list loads
  useEffect(() => {
    if (skills.length > 0) {
      const stillExists = skills.find(s => s.name === selectedSkill?.name)
      if (!stillExists) {
        setSelectedSkill(skills[0])
      } else {
        const updated = skills.find(s => s.name === selectedSkill?.name)
        if (updated) setSelectedSkill(updated)
      }
    } else {
      setSelectedSkill(null)
    }
  }, [skills])

  if (!workspace) {
    return (
      <div className="surfacePanel">
        <div className="emptyState">
          <div className="emptyTitle">No Workspace Selected</div>
          <div className="emptyBody">Select a workspace to view its custom agent skills.</div>
        </div>
      </div>
    )
  }

  return (
    <div className="surfacePanel" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div className="panelHeader" style={{ borderBottom: '1px dashed var(--border)', paddingBottom: '16px', marginBottom: '20px' }}>
        <h2 className="panelTitle">Distilled Agent Skills</h2>
        <p className="panelSubtitle">Procedural workflows, outcome learnings, and constraints packaged by the AI Agent under <code>.agents/skills/</code>.</p>
      </div>

      {error ? (
        <div className="errAlert">Failed to load custom skills: {error}</div>
      ) : busy && skills.length === 0 ? (
        <div className="emptyState">
          <div className="emptyBody">Loading workspace custom skills...</div>
        </div>
      ) : skills.length === 0 ? (
        <div className="emptyState" style={{ padding: '60px 20px' }}>
          <div className="emptyTitle" style={{ fontSize: '15px', color: 'var(--text-main)', marginBottom: '8px', fontFamily: 'var(--font-mono)' }}>No Custom Skills Found</div>
          <div className="emptyBody" style={{ maxWidth: '480px', margin: '0 auto', fontSize: '13px' }}>
            No distilled custom skills were found in this project. When the AI Agent packages a workflow or learns reusable steps, it runs <code>agent-memory distill</code> to save it under <code>.agents/skills/&lt;name&gt;/SKILL.md</code>.
          </div>
        </div>
      ) : (
        <div style={{ display: 'flex', flex: 1, minHeight: 0, gap: '24px', alignItems: 'stretch' }}>
          {/* Left Column: Skill List */}
          <div style={{ width: '280px', flexShrink: 0, borderRight: '1px dashed var(--border)', paddingRight: '16px', display: 'flex', flexDirection: 'column', gap: '8px', overflowY: 'auto' }}>
            <div style={{ fontSize: '10px', fontFamily: 'var(--font-mono)', color: 'var(--text-muted)', textTransform: 'uppercase', marginBottom: '4px', letterSpacing: '0.05em' }}>
              Skills Directory ({skills.length})
            </div>
            {skills.map((skill) => (
              <button
                key={skill.name}
                type="button"
                onClick={() => setSelectedSkill(skill)}
                style={{
                  width: '100%',
                  textAlign: 'left',
                  background: selectedSkill?.name === skill.name ? 'var(--bg-input)' : 'transparent',
                  border: selectedSkill?.name === skill.name ? '1px solid var(--accent-primary)' : '1px solid transparent',
                  padding: '12px',
                  borderRadius: '6px',
                  cursor: 'pointer',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '4px',
                  transition: 'all 0.15s ease'
                }}
              >
                <div style={{ fontWeight: 'bold', fontSize: '13px', color: selectedSkill?.name === skill.name ? 'var(--accent-primary)' : 'var(--text-main)', fontFamily: 'var(--font-mono)' }}>
                  {skill.displayName}
                </div>
                <div style={{ fontSize: '11px', color: 'var(--text-muted)', lineClamp: 2, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>
                  {skill.description || 'No description provided.'}
                </div>
              </button>
            ))}
          </div>

          {/* Right Column: Skill Content Viewer */}
          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', height: '100%', overflowY: 'auto' }}>
            {selectedSkill ? (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '16px', paddingRight: '8px' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', borderBottom: '1px dotted var(--border)', paddingBottom: '12px' }}>
                  <div>
                    <h3 style={{ fontSize: '18px', margin: 0, color: 'var(--text-main)' }}>{selectedSkill.displayName}</h3>
                    <div style={{ fontSize: '11px', fontFamily: 'var(--font-mono)', color: 'var(--text-muted)', marginTop: '4px' }}>
                      Path: {selectedSkill.path}
                    </div>
                  </div>
                </div>

                <div className="markdownViewer" style={{ padding: '16px', background: 'var(--bg-surface)', border: '1px solid var(--border)', borderRadius: '8px' }}>
                  <MarkdownView markdown={selectedSkill.content} clamp={false} theme={theme} />
                </div>
              </div>
            ) : (
              <div className="emptyState" style={{ display: 'flex', height: '100%', alignItems: 'center', justifyContent: 'center' }}>
                <div className="emptyBody">Select a skill from the list to view details.</div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
