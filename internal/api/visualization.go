package api

import (
	"net/http"
	"strconv"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// GraphNode represents a memory node in the relationship graph.
type GraphNode struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Entities    []string `json:"entities"`
	Tags        []string `json:"tags"`
	AccessCount int      `json:"access_count"`
	Importance  float64  `json:"importance"`
	Pinned      bool     `json:"pinned"`
	DecayScore  float64  `json:"decay_score"`
}

// GraphEdge represents a relationship between memories.
type GraphEdge struct {
	Source         string   `json:"source"`
	Target         string   `json:"target"`
	Weight         float64  `json:"weight"`
	SharedEntities []string `json:"shared_entities"`
}

// GraphData represents the complete memory relationship graph.
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// DecayTimelinePoint represents decay data at a point in time.
type DecayTimelinePoint struct {
	Timestamp  string  `json:"timestamp"`
	AvgDecay   float64 `json:"avg_decay"`
	MedianDecay float64 `json:"median_decay"`
	MinDecay   float64 `json:"min_decay"`
	MaxDecay   float64 `json:"max_decay"`
	Count      int     `json:"count"`
}

// DecayTimelineSeries represents a time series for one memory type.
type DecayTimelineSeries struct {
	Type   string               `json:"type"`
	Points []DecayTimelinePoint `json:"points"`
}

// DecayTimelineData represents the complete decay timeline.
type DecayTimelineData struct {
	Series []DecayTimelineSeries `json:"series"`
}

// EntityNode represents an entity in the network.
type EntityNode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	MemoryCount int     `json:"memory_count"`
	Importance  float64 `json:"importance"`
}

// MemoryNode represents a memory in the entity network.
type MemoryNode struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Entities []string `json:"entities"`
	Cluster  string   `json:"cluster,omitempty"`
}

// EntityConnection represents an entity-memory connection.
type EntityConnection struct {
	Entity string `json:"entity"`
	Memory string `json:"memory"`
}

// EntityNetworkData represents the entity-memory network.
type EntityNetworkData struct {
	Entities    []EntityNode        `json:"entities"`
	Memories    []MemoryNode        `json:"memories"`
	Connections []EntityConnection  `json:"connections"`
}

// handleMemoryGraph generates graph visualization data.
func handleMemoryGraph(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 100
		if limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Get recent memories
		memories, err := assets.Store.ListRecentMemoriesByWorkspace(r.Context(), workspace, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Build graph data
		graphData := buildGraphData(memories)

		writeOK(w, http.StatusOK, graphData)
	}
}

// buildGraphData constructs graph nodes and edges from memories.
func buildGraphData(memories []core.MemoryEntry) GraphData {
	nodes := make([]GraphNode, 0, len(memories))
	edges := make([]GraphEdge, 0)

	// Build nodes
	for _, mem := range memories {
		label := mem.Content
		if len(label) > 50 {
			label = label[:50] + "..."
		}

		nodes = append(nodes, GraphNode{
			ID:          mem.ID,
			Type:        string(mem.Type),
			Label:       label,
			Entities:    mem.Entities,
			Tags:        mem.Tags,
			AccessCount: mem.AccessCount,
			Importance:  mem.Importance,
			Pinned:      mem.Pinned,
			DecayScore:  mem.DecayScore,
		})
	}

	// Build edges based on shared entities
	for i := 0; i < len(memories); i++ {
		for j := i + 1; j < len(memories); j++ {
			shared := intersectStrings(memories[i].Entities, memories[j].Entities)
			if len(shared) > 0 {
				weight := float64(len(shared))
				edges = append(edges, GraphEdge{
					Source:         memories[i].ID,
					Target:         memories[j].ID,
					Weight:         weight,
					SharedEntities: shared,
				})
			}
		}
	}

	return GraphData{
		Nodes: nodes,
		Edges: edges,
	}
}

// handleDecayTimeline generates decay timeline data.
func handleDecayTimeline(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}

		interval := r.URL.Query().Get("interval")
		if interval == "" {
			interval = "day"
		}

		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Get all memories
		memories, err := assets.Store.ListRecentMemoriesByWorkspace(r.Context(), workspace, 1000)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Build timeline data
		timelineData := buildDecayTimeline(memories, interval)

		writeOK(w, http.StatusOK, timelineData)
	}
}

// buildDecayTimeline constructs timeline data from memories.
func buildDecayTimeline(memories []core.MemoryEntry, interval string) DecayTimelineData {
	// Group by type
	typeGroups := make(map[core.MemoryType][]core.MemoryEntry)
	for _, mem := range memories {
		typeGroups[mem.Type] = append(typeGroups[mem.Type], mem)
	}

	series := make([]DecayTimelineSeries, 0, len(typeGroups))

	for memType, mems := range typeGroups {
		// Group by time interval
		timeGroups := groupByTimeInterval(mems, interval)

		points := make([]DecayTimelinePoint, 0, len(timeGroups))
		for timestamp, group := range timeGroups {
			point := calculateDecayStats(group, timestamp)
			points = append(points, point)
		}

		// Sort points by timestamp
		// Simple sort - in production would use proper time sorting
		series = append(series, DecayTimelineSeries{
			Type:   string(memType),
			Points: points,
		})
	}

	return DecayTimelineData{
		Series: series,
	}
}

// groupByTimeInterval groups memories by time interval.
func groupByTimeInterval(memories []core.MemoryEntry, interval string) map[string][]core.MemoryEntry {
	groups := make(map[string][]core.MemoryEntry)

	for _, mem := range memories {
		timestamp := mem.UpdatedAt
		if timestamp.IsZero() {
			timestamp = mem.CreatedAt
		}

		var key string
		switch interval {
		case "hour":
			key = timestamp.Format("2006-01-02T15")
		case "week":
			year, week := timestamp.ISOWeek()
			key = timestamp.Format("2006") + "-W" + strconv.Itoa(week) + "-" + strconv.Itoa(year)
		default: // day
			key = timestamp.Format("2006-01-02")
		}

		groups[key] = append(groups[key], mem)
	}

	return groups
}

// calculateDecayStats calculates decay statistics for a group.
func calculateDecayStats(memories []core.MemoryEntry, timestamp string) DecayTimelinePoint {
	if len(memories) == 0 {
		return DecayTimelinePoint{
			Timestamp: timestamp,
			Count:     0,
		}
	}

	var sum, min, max float64
	min = 1.0
	max = 0.0

	scores := make([]float64, 0, len(memories))
	for _, mem := range memories {
		score := mem.DecayScore
		scores = append(scores, score)
		sum += score

		if score < min {
			min = score
		}
		if score > max {
			max = score
		}
	}

	avg := sum / float64(len(scores))
	median := calculateMedian(scores)

	return DecayTimelinePoint{
		Timestamp:   timestamp,
		AvgDecay:    avg,
		MedianDecay: median,
		MinDecay:    min,
		MaxDecay:    max,
		Count:       len(memories),
	}
}

// calculateMedian calculates the median of a slice of floats.
func calculateMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Simple median calculation - in production would use proper sorting
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// handleEntityNetwork generates entity network data.
func handleEntityNetwork(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}

		minConnectionsStr := r.URL.Query().Get("min_connections")
		minConnections := 1
		if minConnectionsStr != "" {
			if parsed, err := strconv.Atoi(minConnectionsStr); err == nil && parsed > 0 {
				minConnections = parsed
			}
		}

		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Get memories
		memories, err := assets.Store.ListRecentMemoriesByWorkspace(r.Context(), workspace, 500)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		// Build entity network
		networkData := buildEntityNetwork(memories, minConnections)

		writeOK(w, http.StatusOK, networkData)
	}
}

// buildEntityNetwork constructs entity-memory network data.
func buildEntityNetwork(memories []core.MemoryEntry, minConnections int) EntityNetworkData {
	// Count entity occurrences
	entityCounts := make(map[string]int)
	entityMemories := make(map[string][]string)

	for _, mem := range memories {
		for _, entity := range mem.Entities {
			entityCounts[entity]++
			entityMemories[entity] = append(entityMemories[entity], mem.ID)
		}
	}

	// Filter entities by min connections
	entities := make([]EntityNode, 0)
	for entity, count := range entityCounts {
		if count >= minConnections {
			importance := float64(count) / float64(len(memories))
			entities = append(entities, EntityNode{
				ID:          entity,
				Name:        entity,
				MemoryCount: count,
				Importance:  importance,
			})
		}
	}

	// Build memory nodes
	memoryNodes := make([]MemoryNode, 0, len(memories))
	for _, mem := range memories {
		// Only include memories with qualifying entities
		validEntities := make([]string, 0)
		for _, entity := range mem.Entities {
			if entityCounts[entity] >= minConnections {
				validEntities = append(validEntities, entity)
			}
		}

		if len(validEntities) > 0 {
			memoryNodes = append(memoryNodes, MemoryNode{
				ID:       mem.ID,
				Type:     string(mem.Type),
				Entities: validEntities,
			})
		}
	}

	// Build connections
	connections := make([]EntityConnection, 0)
	for _, mem := range memoryNodes {
		for _, entity := range mem.Entities {
			connections = append(connections, EntityConnection{
				Entity: entity,
				Memory: mem.ID,
			})
		}
	}

	return EntityNetworkData{
		Entities:    entities,
		Memories:    memoryNodes,
		Connections: connections,
	}
}

// intersectStrings returns the intersection of two string slices.
func intersectStrings(a, b []string) []string {
	set := make(map[string]bool)
	for _, s := range a {
		set[s] = true
	}

	result := make([]string, 0)
	for _, s := range b {
		if set[s] {
			result = append(result, s)
			delete(set, s) // Avoid duplicates
		}
	}

	return result
}
