// Package auditlogger provides an example lifecycle plugin that logs all memory operations.
package auditlogger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/plugin"
)

// AuditLogPlugin logs all memory lifecycle events to a file or stdout.
type AuditLogPlugin struct {
	*plugin.BaseLifecyclePlugin
	logger   *log.Logger
	file     *os.File
	jsonMode bool
}

// AuditEvent represents a logged audit event.
type AuditEvent struct {
	Timestamp string                 `json:"timestamp"`
	Event     string                 `json:"event"`
	Details   map[string]interface{} `json:"details"`
}

// NewAuditLogPlugin creates a new audit log plugin.
func NewAuditLogPlugin() *AuditLogPlugin {
	return &AuditLogPlugin{
		BaseLifecyclePlugin: plugin.NewBaseLifecyclePlugin(
			"audit-logger",
			"1.0.0",
			"Logs all memory operations for audit purposes",
		),
	}
}

// Initialize initializes the plugin with configuration.
// Config options:
//   - logFile (string): Path to log file (default: stdout)
//   - jsonMode (bool): Use JSON format (default: false)
func (p *AuditLogPlugin) Initialize(ctx context.Context, config map[string]any) error {
	// Get log file path
	logFile, _ := config["logFile"].(string)

	// Get JSON mode
	jsonMode, _ := config["jsonMode"].(bool)
	p.jsonMode = jsonMode

	// Open log file or use stdout
	var writer *os.File
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		writer = f
		p.file = f
	} else {
		writer = os.Stdout
	}

	// Create logger
	if p.jsonMode {
		p.logger = log.New(writer, "", 0)
	} else {
		p.logger = log.New(writer, "[AUDIT] ", log.LstdFlags)
	}

	return nil
}

// Shutdown closes the log file if opened.
func (p *AuditLogPlugin) Shutdown(ctx context.Context) error {
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

// OnWrite logs memory write events.
func (p *AuditLogPlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
	if p.jsonMode {
		return p.logJSON("memory.write", map[string]interface{}{
			"memory_id": mem.ID,
			"workspace": mem.Workspace,
			"type":      mem.Type,
			"content":   truncate(mem.Content, 100),
		})
	}

	p.logger.Printf("WRITE: memory_id=%s workspace=%s type=%s content=%q",
		mem.ID, mem.Workspace, mem.Type, truncate(mem.Content, 50))
	return nil
}

// OnWriteComplete logs successful memory writes.
func (p *AuditLogPlugin) OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error {
	if p.jsonMode {
		return p.logJSON("memory.write.complete", map[string]interface{}{
			"memory_id": mem.ID,
			"workspace": mem.Workspace,
		})
	}

	p.logger.Printf("WRITE_COMPLETE: memory_id=%s workspace=%s", mem.ID, mem.Workspace)
	return nil
}

// OnRetrieve logs memory retrieval operations.
func (p *AuditLogPlugin) OnRetrieve(ctx context.Context, query string, workspace string) error {
	if p.jsonMode {
		return p.logJSON("memory.retrieve", map[string]interface{}{
			"query":     truncate(query, 100),
			"workspace": workspace,
		})
	}

	p.logger.Printf("RETRIEVE: query=%q workspace=%s", truncate(query, 50), workspace)
	return nil
}

// OnRetrieveComplete logs retrieval results.
func (p *AuditLogPlugin) OnRetrieveComplete(ctx context.Context, query string, hits int) error {
	if p.jsonMode {
		return p.logJSON("memory.retrieve.complete", map[string]interface{}{
			"query": truncate(query, 100),
			"hits":  hits,
		})
	}

	p.logger.Printf("RETRIEVE_COMPLETE: query=%q hits=%d", truncate(query, 50), hits)
	return nil
}

// OnDelete logs memory deletion.
func (p *AuditLogPlugin) OnDelete(ctx context.Context, memoryID string) error {
	if p.jsonMode {
		return p.logJSON("memory.delete", map[string]interface{}{
			"memory_id": memoryID,
		})
	}

	p.logger.Printf("DELETE: memory_id=%s", memoryID)
	return nil
}

// OnDecay logs decay operations.
func (p *AuditLogPlugin) OnDecay(ctx context.Context, workspace string, count int) error {
	if p.jsonMode {
		return p.logJSON("memory.decay", map[string]interface{}{
			"workspace": workspace,
			"count":     count,
		})
	}

	p.logger.Printf("DECAY: workspace=%s count=%d", workspace, count)
	return nil
}

// logJSON logs an event in JSON format.
func (p *AuditLogPlugin) logJSON(event string, details map[string]interface{}) error {
	auditEvent := AuditEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Event:     event,
		Details:   details,
	}

	data, err := json.Marshal(auditEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	p.logger.Println(string(data))
	return nil
}

// truncate truncates a string to maxLen bytes without splitting a UTF-8 rune.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return core.TruncateUTF8(s, maxLen) + "..."
}
