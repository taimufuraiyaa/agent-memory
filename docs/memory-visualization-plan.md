# Memory Visualization Implementation Plan

**Task:** 4.2 - Add Memory Visualization  
**Status:** Planned (Ready for Implementation)  
**Priority:** 4 (Nice to Have / Future Enhancement)

## Overview

Add four comprehensive visualizations to the agent-memory dashboard to provide visual insights into memory patterns, relationships, performance, and decay dynamics.

## Visualization Requirements

### 1. Graph Visualization - Memory Relationship Network

**Purpose:** Visualize connections between memories based on shared entities, tags, and temporal relationships.

**Technology:** D3.js force-directed graph (already installed in dashboard)

**Features:**
- Node: Each memory entry
- Node size: Proportional to access_count or importance
- Node color: Memory type (semantic, procedural, episodic, outcome)
- Edges: Connect memories with shared entities/tags
- Edge thickness: Strength of relationship (shared entity count)
- Interactive: Click to view memory details, hover for tooltips
- Zoom and pan: D3 zoom behavior
- Filtering: By type, tier, workspace

**Data Requirements:**
- List of memories with entities and tags
- Entity/tag overlap calculations
- Memory metadata (type, access_count, importance)

**API Endpoint:**
```typescript
GET /api/v1/memories/graph?workspace=<name>&limit=100
Response: {
  nodes: Array<{
    id: string
    type: MemoryType
    label: string  // truncated content
    entities: string[]
    tags: string[]
    access_count: number
    importance: number
    pinned: boolean
  }>
  edges: Array<{
    source: string  // memory id
    target: string  // memory id
    weight: number  // relationship strength
    shared_entities: string[]
  }>
}
```

**React Component:**
```typescript
// src/ui/charts/MemoryNetworkGraph.tsx
interface MemoryNetworkGraphProps {
  workspace: string
  typeFilter?: MemoryType[]
  onNodeClick?: (memory: MemoryNode) => void
}

export function MemoryNetworkGraph(props: MemoryNetworkGraphProps) {
  // D3 force simulation
  // Interactive node positioning
  // Zoom/pan controls
  // Legend
}
```

**D3 Implementation:**
- Use `d3-force` for force-directed layout
- `forceSimulation()` with:
  - `forceLink()` for edges
  - `forceManyBody()` for node repulsion
  - `forceCenter()` for centering
  - `forceCollide()` to prevent overlap
- SVG rendering with React refs
- Color scale from existing `--chart-1` through `--chart-6` CSS variables

### 2. Decay Timeline Chart

**Purpose:** Show how decay scores evolve over time for different memory types.

**Technology:** D3.js line chart with area fill

**Features:**
- X-axis: Time (created_at to present)
- Y-axis: Decay score (0.0 to 1.0)
- Multiple lines: One per memory type
- Area fill: Below each line with transparency
- Interactive: Hover to see exact values, tooltip with memory details
- Legend: Memory types with colors
- Grid lines: For easy reading
- Zoom: Time range selector

**Data Requirements:**
- Memories with decay_score and timestamps
- Historical decay score snapshots (if available)
- Aggregated decay scores by type and time period

**API Endpoint:**
```typescript
GET /api/v1/memories/decay-timeline?workspace=<name>&from=<date>&to=<date>&interval=<hour|day|week>
Response: {
  series: Array<{
    type: MemoryType
    points: Array<{
      timestamp: string
      avg_decay: number
      median_decay: number
      min_decay: number
      max_decay: number
      count: number
    }>
  }>
}
```

**React Component:**
```typescript
// src/ui/charts/DecayTimelineChart.tsx
interface DecayTimelineChartProps {
  workspace: string
  timeRange?: { from: string; to: string }
  interval?: 'hour' | 'day' | 'week'
}

export function DecayTimelineChart(props: DecayTimelineChartProps) {
  // D3 time scale for X-axis
  // Linear scale for Y-axis (0-1)
  // Line generator with curve
  // Area generator
  // Interactive tooltip
  // Legend
}
```

**D3 Implementation:**
- Use `d3-scale` for time (scaleTime) and linear (scaleLinear) scales
- `d3-axis` for axis rendering
- `d3-shape` for line() and area() generators
- `d3-time` for time formatting
- Brush selection for zooming (`d3-brush`)
- Transition animations on data updates

### 3. Token Budget Utilization Graph

**Purpose:** Visualize token usage vs budget across different operations and time.

**Technology:** D3.js stacked area chart and bar chart combo

**Features:**
- **Primary View: Stacked Area Chart**
  - X-axis: Operations (search, recall, write) or time
  - Y-axis: Token count
  - Stacked areas: returned_tokens, baseline_tokens
  - Highlight: saved_tokens as difference
  - Budget line: Horizontal line showing token_budget
  - Percentage overlay: Savings percentage

- **Secondary View: Bar Chart**
  - Grouped bars per operation
  - Blue: returned_tokens
  - Gray: baseline_tokens
  - Green: saved_tokens (difference)
  - Labels: Percentages

**Data Requirements:**
- Token metrics from dashboard stats
- Per-operation breakdown
- Historical token usage over time (if available)

**API Endpoint:** (Already exists in `/api/v1/stats`)
```typescript
// Use existing DashboardStats
stats.token_metrics_by_operation // Current data
stats.token_metrics_by_group      // Grouped data

// New endpoint for historical data:
GET /api/v1/stats/token-history?workspace=<name>&from=<date>&to=<date>&interval=<hour|day|week>
Response: {
  timeline: Array<{
    timestamp: string
    operations: Array<{
      operation: string
      returned_tokens: number
      baseline_tokens: number
      saved_tokens: number
      budget: number
    }>
  }>
}
```

**React Component:**
```typescript
// src/ui/charts/TokenUtilizationChart.tsx
interface TokenUtilizationChartProps {
  stats: DashboardStats
  viewMode?: 'area' | 'bar'
  showHistory?: boolean
}

export function TokenUtilizationChart(props: TokenUtilizationChartProps) {
  // Stacked area chart
  // Bar chart alternative
  // Budget threshold line
  // Savings highlight
  // Legend
  // View mode toggle
}
```

**D3 Implementation:**
- `d3-shape` stack() for stacked layout
- `d3-scale` for scales (band, linear)
- Color scheme: Use theme variables
- Annotations for budget line
- Responsive sizing
- Tooltip showing exact values and percentages

### 4. Memory Relationship Network Diagram

**Purpose:** Advanced network visualization showing entity connections across memories.

**Technology:** Cytoscape.js (already installed) or D3 hierarchical layout

**Features:**
- **Cytoscape Approach:**
  - Entity-memory bipartite graph
  - Circular or hierarchical layout
  - Entity nodes (larger, distinct color)
  - Memory nodes (smaller, type-colored)
  - Edges: Entity-to-memory connections
  - Clustering: Group by entity or type
  - Search: Find paths between memories

- **D3 Hierarchical Approach:**
  - Tree or radial tree layout
  - Root: Workspace
  - Level 1: Memory types
  - Level 2: Entities
  - Level 3: Individual memories
  - Collapsible nodes
  - Zoom to focus

**Data Requirements:**
- Complete entity-memory mapping
- Entity frequency and importance
- Memory clusters (if available)

**API Endpoint:**
```typescript
GET /api/v1/memories/entity-network?workspace=<name>&min_connections=2
Response: {
  entities: Array<{
    id: string
    name: string
    memory_count: number
    importance: number
  }>
  memories: Array<{
    id: string
    type: MemoryType
    entities: string[]
    cluster?: string
  }>
  connections: Array<{
    entity: string
    memory: string
  }>
  clusters?: Array<{
    id: string
    label: string
    memory_ids: string[]
  }>
}
```

**React Component:**
```typescript
// src/ui/charts/EntityNetworkDiagram.tsx
interface EntityNetworkDiagramProps {
  workspace: string
  layout?: 'force' | 'circular' | 'hierarchical'
  minConnections?: number
  onMemorySelect?: (memoryId: string) => void
}

export function EntityNetworkDiagram(props: EntityNetworkDiagramProps) {
  // Cytoscape initialization
  // Layout selection
  // Node styling
  // Event handlers
  // Controls (zoom, layout, filter)
}
```

**Implementation Options:**

**Option A: Cytoscape.js**
```typescript
import cytoscape from 'cytoscape'

// Initialize
const cy = cytoscape({
  container: containerRef.current,
  elements: {
    nodes: [...entityNodes, ...memoryNodes],
    edges: connections
  },
  style: [
    {
      selector: 'node[type="entity"]',
      style: {
        'background-color': '#4a9eff',
        'label': 'data(name)',
        'width': 'data(size)',
        'height': 'data(size)'
      }
    },
    {
      selector: 'node[type="memory"]',
      style: {
        'background-color': 'data(color)',
        'width': 20,
        'height': 20
      }
    }
  ],
  layout: {
    name: 'cose',  // Force-directed layout
    animate: true
  }
})
```

**Option B: D3 Radial Tree**
```typescript
import * as d3 from 'd3'

const tree = d3.tree()
  .size([2 * Math.PI, radius])
  .separation((a, b) => (a.parent === b.parent ? 1 : 2) / a.depth)

// Radial projection
const radialPoint = (x, y) => {
  return [y * Math.cos(x - Math.PI / 2), y * Math.sin(x - Math.PI / 2)]
}
```

## Integration with Dashboard

### New Surface: 'visualizations'

Add a new surface option to the dashboard:

```typescript
type Surface = 'overview' | 'search' | 'recall' | 'diagnostics' | 'sessions' | 'benchmark' | 'wiki' | 'lifecycle' | 'visualizations'
```

### Navigation

Add visualization button to main navigation:

```typescript
<button onClick={() => setSurface('visualizations')}>
  📊 Visualizations
</button>
```

### Visualization Dashboard Layout

```typescript
{surface === 'visualizations' && (
  <div className="visualizations-surface">
    <div className="viz-controls">
      <select value={workspace} onChange={...}>
        {projects.map(p => <option>{p.name}</option>)}
      </select>
      <select value={vizMode} onChange={...}>
        <option value="network">Memory Network</option>
        <option value="decay">Decay Timeline</option>
        <option value="tokens">Token Utilization</option>
        <option value="entities">Entity Network</option>
      </select>
    </div>
    
    <div className="viz-container">
      {vizMode === 'network' && <MemoryNetworkGraph workspace={workspace} />}
      {vizMode === 'decay' && <DecayTimelineChart workspace={workspace} />}
      {vizMode === 'tokens' && <TokenUtilizationChart stats={stats} />}
      {vizMode === 'entities' && <EntityNetworkDiagram workspace={workspace} />}
    </div>
  </div>
)}
```

## Backend API Changes

### New Endpoints Required

1. **Memory Graph Data**
   - `GET /api/v1/memories/graph`
   - Compute entity/tag overlaps
   - Return node and edge data
   - Support filtering and limits

2. **Decay Timeline Data**
   - `GET /api/v1/memories/decay-timeline`
   - Aggregate decay scores by type and time
   - Support time range and interval
   - Return time series data

3. **Token History Data** (Optional)
   - `GET /api/v1/stats/token-history`
   - Historical token metrics
   - Requires periodic snapshots (future enhancement)
   - Return timeline data

4. **Entity Network Data**
   - `GET /api/v1/memories/entity-network`
   - Build entity-memory bipartite graph
   - Compute entity importance
   - Support min_connections filter

### Go Implementation Location

```
internal/api/
├── routes.go           # Register new routes
├── handlers.go         # Add visualization handlers
└── visualization/
    ├── graph.go        # Memory graph generation
    ├── decay.go        # Decay timeline aggregation
    ├── tokens.go       # Token history (future)
    └── entities.go     # Entity network generation
```

## Styling

Add to `src/ui/styles.css`:

```css
/* Visualization Surface */
.visualizations-surface {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.viz-controls {
  padding: 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  gap: 12px;
  align-items: center;
}

.viz-container {
  flex: 1;
  overflow: hidden;
  position: relative;
  padding: 24px;
}

/* Network Graph */
.memory-network-svg {
  width: 100%;
  height: 100%;
}

.network-node {
  cursor: pointer;
  transition: all 0.2s ease;
}

.network-node:hover {
  stroke: var(--accent);
  stroke-width: 3px;
}

.network-edge {
  stroke: var(--border);
  stroke-opacity: 0.6;
}

.network-label {
  font-size: 11px;
  pointer-events: none;
  user-select: none;
}

/* Decay Timeline */
.decay-chart-svg {
  width: 100%;
  height: 100%;
}

.decay-line {
  fill: none;
  stroke-width: 2px;
}

.decay-area {
  opacity: 0.2;
}

.decay-axis {
  font-size: 12px;
}

.decay-axis-label {
  font-size: 13px;
  font-weight: 500;
}

.decay-grid-line {
  stroke: var(--border);
  stroke-opacity: 0.1;
  stroke-dasharray: 2,2;
}

/* Token Utilization */
.token-chart-svg {
  width: 100%;
  height: 100%;
}

.token-bar {
  transition: opacity 0.2s ease;
}

.token-bar:hover {
  opacity: 0.8;
}

.token-budget-line {
  stroke: var(--warn);
  stroke-width: 2px;
  stroke-dasharray: 5,5;
}

.token-label {
  font-size: 12px;
  font-weight: 500;
}

/* Entity Network */
.entity-network-container {
  width: 100%;
  height: 100%;
  position: relative;
}

.entity-network-controls {
  position: absolute;
  top: 12px;
  right: 12px;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  z-index: 10;
}

/* Chart Legend */
.viz-legend {
  position: absolute;
  top: 12px;
  left: 12px;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  font-size: 13px;
}

.viz-legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.viz-legend-color {
  width: 16px;
  height: 16px;
  border-radius: 3px;
}

/* Tooltip */
.viz-tooltip {
  position: absolute;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  font-size: 13px;
  pointer-events: none;
  z-index: 100;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  max-width: 300px;
}

.viz-tooltip-title {
  font-weight: 500;
  margin-bottom: 6px;
  color: var(--text);
}

.viz-tooltip-content {
  color: var(--text-secondary);
  line-height: 1.4;
}
```

## Testing Plan

### Unit Tests

```typescript
// Memory graph data generation
describe('generateMemoryGraph', () => {
  it('should create nodes for all memories', () => {})
  it('should create edges for shared entities', () => {})
  it('should filter by memory type', () => {})
  it('should limit node count', () => {})
})

// Decay timeline aggregation
describe('aggregateDecayTimeline', () => {
  it('should group by time interval', () => {})
  it('should calculate statistics per type', () => {})
  it('should handle empty data', () => {})
})

// Entity network generation
describe('generateEntityNetwork', () => {
  it('should identify all entities', () => {})
  it('should create bipartite graph', () => {})
  it('should filter by connection count', () => {})
})
```

### Integration Tests

```typescript
// API endpoint tests
describe('GET /api/v1/memories/graph', () => {
  it('should return graph data', async () => {})
  it('should filter by workspace', async () => {})
  it('should limit results', async () => {})
  it('should handle missing workspace', async () => {})
})

// Component tests
describe('<MemoryNetworkGraph />', () => {
  it('should render SVG', () => {})
  it('should render nodes', () => {})
  it('should handle node click', () => {})
  it('should show tooltip on hover', () => {})
})
```

### E2E Tests

```typescript
// Full workflow tests
describe('Visualizations Surface', () => {
  it('should navigate to visualizations', () => {})
  it('should switch between viz modes', () => {})
  it('should interact with charts', () => {})
  it('should update on workspace change', () => {})
})
```

## Performance Considerations

### Data Optimization

- **Limit nodes**: Default max 100-200 nodes for network graphs
- **Sampling**: For large datasets, sample representative memories
- **Caching**: Cache graph computations server-side
- **Incremental loading**: Load on-demand as user interacts

### Rendering Optimization

- **Canvas fallback**: For >500 nodes, use Canvas instead of SVG
- **Debouncing**: Debounce zoom/pan events
- **Virtual scrolling**: For timeline with many data points
- **Memoization**: React.memo for chart components
- **Web Workers**: Offload heavy computations (force simulation)

### API Optimization

- **Pagination**: Support cursor-based pagination
- **Selective fields**: Return only needed fields
- **Compression**: gzip response bodies
- **ETag caching**: Cache responses with ETag headers

## Implementation Phases

### Phase 1: Foundation (Week 1)
- ✅ Plan and design (this document)
- [ ] Create API endpoints (graph, decay, entities)
- [ ] Add routes and handlers
- [ ] Basic data generation logic
- [ ] Unit tests for data generation

### Phase 2: Components (Week 2)
- [ ] Create React chart components
- [ ] Basic D3 rendering
- [ ] Styling and theming
- [ ] Interactive tooltips
- [ ] Component unit tests

### Phase 3: Integration (Week 3)
- [ ] Add visualization surface to dashboard
- [ ] Connect components to APIs
- [ ] Navigation and controls
- [ ] State management
- [ ] Integration tests

### Phase 4: Polish (Week 4)
- [ ] Performance optimization
- [ ] Responsive design
- [ ] Accessibility (ARIA labels, keyboard nav)
- [ ] Documentation
- [ ] E2E tests

## Dependencies

Already installed:
- ✅ `d3` - Full D3 suite
- ✅ `cytoscape` - Graph visualization
- ✅ `react` - UI framework
- ✅ `typescript` - Type safety

No additional dependencies needed!

## Documentation

### User Guide

Create `docs/visualization-guide.md`:
- How to access visualizations
- Interpreting each visualization
- Interaction guide (zoom, pan, filter)
- Use cases and examples

### Developer Guide

Add to this document:
- Component API reference
- Backend API specifications
- Extending visualizations
- Custom layouts and themes

## Success Metrics

### Functionality
- ✅ All 4 visualizations implemented
- ✅ Interactive and responsive
- ✅ No console errors
- ✅ All tests passing

### Performance
- Graph rendering: <1s for 100 nodes
- Chart updates: <200ms
- API responses: <500ms
- Smooth interactions: 60fps

### Usability
- Intuitive navigation
- Clear legends and labels
- Helpful tooltips
- Responsive to screen size
- Accessible (WCAG AA)

## Future Enhancements

### Advanced Features
- Export visualizations as PNG/SVG
- Share visualization links
- Custom time ranges with date picker
- Real-time updates (WebSocket)
- Animation playback (time-lapse)
- 3D graph visualization (Three.js)
- Heatmaps for access patterns
- Comparison view (side-by-side workspaces)

### Analytics
- Identify memory clusters
- Detect anomalies in decay patterns
- Predict token usage trends
- Recommend optimization opportunities
- Generate insights automatically

## Conclusion

This implementation plan provides a comprehensive blueprint for adding memory visualization to agent-memory. The visualizations will provide valuable insights into memory patterns, relationships, and performance, significantly enhancing the user experience and system observability.

**Estimated Effort:** 3-4 weeks full-time development
**Technical Complexity:** High (D3.js, graph algorithms, React integration)
**User Value:** High (visual insights, pattern discovery, debugging)
**Dependencies:** None (all libraries already installed)
**Risk:** Low (well-defined scope, proven technologies)

**Status:** Ready for implementation ✅
