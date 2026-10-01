import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const workspaceAppSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8').catch(() => '')
const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const chatSource = await readFile(new URL('../src/ui/workspace/WorkspaceChatView.tsx', import.meta.url), 'utf8')

const explorerSource = await readFile(new URL('../src/ui/workspace/WorkspaceExplorer.tsx', import.meta.url), 'utf8')

test('workspace explorer replaces duplicated destination navigation', () => {
  assert.match(workspaceAppSource, /<AppShell\.Navbar[^>]*aria-label="Workspace explorer"/)
  assert.match(workspaceAppSource, /<WorkspaceExplorer[\s\S]*onSelectWorkspace=\{selectWorkspace\}/)
  assert.match(workspaceAppSource, /data-shell="vscode"/)
  assert.doesNotMatch(workspaceAppSource, /data-workspace-picker|<NavLink/)
  for (const destination of ['sources', 'memories', 'history', 'notes', 'activity', 'settings']) {
    assert.match(explorerSource, new RegExp(`onWorkspaceAction\\(id, '${destination}'\\)`))
  }
})

test('chat keeps Recents, Skills, and Overview in its header', () => {
  assert.match(workspaceAppSource, /aria-label="Chat workspace views"/)
  assert.match(workspaceAppSource, /\['recents', 'skills', 'overview'\] as const/)
  assert.match(workspaceAppSource, /aria-pressed=\{contextDrawerOpen && currentPanel === panel\}/)
})

test('active chat uses the full canvas with a bottom composer and on-demand context drawer', async () => {
  const css = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')
  assert.match(workspaceAppSource, /workspaceMain--chat/)
  assert.match(workspaceAppSource, /<Drawer opened=\{contextDrawerOpen\} onClose=\{\(\) => setContextDrawerOpen\(false\)\} position="right"/)
  assert.doesNotMatch(workspaceAppSource, /<aside className="workspaceContextPanel"/)
  assert.match(chatSource, /className="workspaceChatTranscript"/)
  assert.match(css, /\.workspaceChatTranscript \{[^}]*overflow-y: auto/s)
  assert.match(css, /\.workspaceChatComposer \{[^}]*position: sticky;[^}]*bottom: 0/s)
})

test('chat uses one workspace scope without a second source picker or repeated scope copy', () => {
  assert.match(chatSource, /const scope: WorkspaceScope = \{ workspaceId \}/)
  assert.match(chatSource, /gateway\.ask\(scope, turn\.question/)
  assert.doesNotMatch(chatSource, /gateway\.listSources|<Select|sourceId|All workspace sources/)
  assert.match(chatSource, /Chats are stored in this browser for this workspace\./)
  assert.match(chatSource, /aria-label="Message this workspace"/)
  assert.match(chatSource, /Ask a question/)
  assert.match(chatSource, /placeholder="Type your message…"/)
  assert.match(chatSource, /<ActionIcon type="submit" aria-label="Send message"/)
  assert.doesNotMatch(chatSource, /title="Ask failed"/)
  assert.match(chatSource, /errorsByTurn\[turn\.id\]/)
  assert.doesNotMatch(chatSource, /Ask \{workspaceId\}|Ask about \$\{workspaceId\}|Your questions search only this workspace/)
  assert.match(workspaceAppSource, /className="workspaceHeaderContext"[^>]*>\{workspace\?\.name \|\| 'Loading workspace'\}<\/Text>/)
  assert.doesNotMatch(workspaceAppSource, /workspaceHeaderContext[^\n]*activeWorkItem\.title/)
  assert.doesNotMatch(workspaceAppSource, /<Text className="workspaceEyebrow"[^>]*>\{workspace\.name\}<\/Text><Title order=\{1\}>\{activeChat\.title\}<\/Title>/)
})

test('workspace actions create scoped chats and saved searches', () => {
  assert.match(explorerSource, /onWorkspaceAction\(id, 'new-chat'\)\}>New chat/)
  assert.match(explorerSource, /onWorkspaceAction\(id, 'new-search'\)\}>New search/)
  assert.match(workspaceAppSource, /createChatRecord\(targetWorkspaceId\)/)
  assert.match(workspaceAppSource, /createSearchRecord\(targetWorkspaceId\)/)
  assert.match(chatSource, /gateway\.ask\(scope, turn\.question/)
  assert.match(chatSource, /aria-label="Message this workspace"/)
})

test('both runtimes are inputs to one shell instead of separate navigation contracts', () => {
  assert.match(workspaceAppSource, /DashboardRuntime/)
  assert.match(workspaceAppSource, /KnowledgeGateway/)
  assert.doesNotMatch(workspaceAppSource, /HostedApp|<App\s/)
})

test('canonical shell is the only mounted workspace without changing stored data', () => {
  assert.doesNotMatch(mainSource, /VITE_UNIFIED_WORKSPACE_ENABLED|<HostedApp|<App\s/)
  assert.match(mainSource, /<WorkspaceApp runtime=\{runtime\} gateway=\{gateway\}/)
  assert.match(mainSource, /createStandaloneKnowledgeGateway/)
  assert.match(mainSource, /<HostedWorkspaceBootstrap runtime=\{runtime\}/)
})
