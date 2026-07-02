# Audit Logger Plugin

An example lifecycle plugin that logs all memory operations for audit and debugging purposes.

## Features

- Logs all memory lifecycle events (write, retrieve, delete, decay)
- Supports text and JSON output formats
- Can log to file or stdout
- Configurable via Initialize() method

## Usage

```go
package main

import (
    "context"
    "github.com/taimufuraiyaa/agent-memory/examples/plugins/audit-logger"
    "github.com/taimufuraiyaa/agent-memory/internal/plugin"
)

func main() {
    // Create plugin
    auditPlugin := auditlogger.NewAuditLogPlugin()
    
    // Register with global registry
    registry := plugin.GetRegistry()
    err := registry.Register(auditPlugin, plugin.PluginMetadata{
        Name:        "audit-logger",
        Version:     "1.0.0",
        Type:        plugin.PluginTypeLifecycle,
        Description: "Logs all memory operations",
        Author:      "agent-memory",
        License:     "MIT",
    })
    if err != nil {
        panic(err)
    }
    
    // Initialize with configuration
    config := map[string]any{
        "logFile":  "/var/log/agent-memory/audit.log",
        "jsonMode": true,
    }
    err = auditPlugin.Initialize(context.Background(), config)
    if err != nil {
        panic(err)
    }
    
    // Plugin is now active and will log all memory operations
}
```

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `logFile` | string | stdout | Path to log file (empty = stdout) |
| `jsonMode` | bool | false | Use JSON format for logs |

## Example Output

### Text Format (default)

```
[AUDIT] 2024/01/15 10:30:45 WRITE: memory_id=mem_abc123 workspace=default type=semantic content="User prefers JSON format"
[AUDIT] 2024/01/15 10:30:46 WRITE_COMPLETE: memory_id=mem_abc123 workspace=default
[AUDIT] 2024/01/15 10:31:10 RETRIEVE: query="user preferences" workspace=default
[AUDIT] 2024/01/15 10:31:10 RETRIEVE_COMPLETE: query="user preferences" hits=3
[AUDIT] 2024/01/15 10:35:00 DELETE: memory_id=mem_xyz789
[AUDIT] 2024/01/15 11:00:00 DECAY: workspace=default count=42
```

### JSON Format

```json
{"timestamp":"2024-01-15T10:30:45Z","event":"memory.write","details":{"memory_id":"mem_abc123","workspace":"default","type":"semantic","content":"User prefers JSON format"}}
{"timestamp":"2024-01-15T10:30:46Z","event":"memory.write.complete","details":{"memory_id":"mem_abc123","workspace":"default"}}
{"timestamp":"2024-01-15T10:31:10Z","event":"memory.retrieve","details":{"query":"user preferences","workspace":"default"}}
{"timestamp":"2024-01-15T10:31:10Z","event":"memory.retrieve.complete","details":{"query":"user preferences","hits":3}}
{"timestamp":"2024-01-15T10:35:00Z","event":"memory.delete","details":{"memory_id":"mem_xyz789"}}
{"timestamp":"2024-01-15T11:00:00Z","event":"memory.decay","details":{"workspace":"default","count":42}}
```

## Use Cases

1. **Audit Compliance**: Track all memory operations for regulatory compliance
2. **Debugging**: Understand memory access patterns and identify issues
3. **Monitoring**: Feed logs to monitoring systems (ELK, Splunk, etc.)
4. **Analytics**: Analyze memory usage patterns over time
5. **Security**: Detect suspicious memory access patterns

## Integration with Monitoring Systems

### Splunk

```bash
# Configure Splunk to monitor the log file
splunk add monitor /var/log/agent-memory/audit.log -sourcetype json_no_timestamp
```

### ELK Stack

```yaml
# Filebeat configuration
filebeat.inputs:
- type: log
  enabled: true
  paths:
    - /var/log/agent-memory/audit.log
  json.keys_under_root: true
  json.add_error_key: true
```

### CloudWatch

```go
// Use AWS CloudWatch Logs SDK to stream logs
import "github.com/aws/aws-sdk-go/service/cloudwatchlogs"

// Configure logFile to write to a local buffer
// Use CloudWatch Logs PutLogEvents API to stream
```

## Performance Considerations

- File I/O is buffered for performance
- Text format is faster than JSON
- Consider async logging for high-throughput scenarios
- Use log rotation for long-running deployments

## Customization

Extend the plugin to add custom behavior:

```go
type CustomAuditLogger struct {
    *auditlogger.AuditLogPlugin
    notifier Notifier
}

func (p *CustomAuditLogger) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
    // Call parent
    if err := p.AuditLogPlugin.OnWrite(ctx, mem); err != nil {
        return err
    }
    
    // Custom notification
    if isSensitive(mem) {
        p.notifier.Alert("Sensitive memory written: " + mem.ID)
    }
    
    return nil
}
```
