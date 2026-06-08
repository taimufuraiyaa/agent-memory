# Security Guide

This document provides comprehensive security guidance for using and developing agent-memory.

## Table of Contents

- [Security Model](#security-model)
- [Data Protection](#data-protection)
- [Secret and PII Filtering](#secret-and-pii-filtering)
- [Network Security](#network-security)
- [Development Security](#development-security)
- [Deployment Security](#deployment-security)
- [Threat Model](#threat-model)

## Security Model

### Local-First Architecture

agent-memory follows a **local-first** security model:

```
┌─────────────────────────────────────┐
│  Your Machine (Local)               │
│                                     │
│  ┌───────────────────────────────┐ │
│  │ agent-memory CLI              │ │
│  │ - Reads/writes local DB       │ │
│  │ - Runs local embeddings       │ │
│  │ - Serves local dashboard      │ │
│  └───────────────────────────────┘ │
│             │                        │
│             ▼                        │
│  ┌───────────────────────────────┐ │
│  │ ~/.agent-memory/              │ │
│  │ - SQLite databases            │ │
│  │ - Embedding models            │ │
│  │ - Configuration files         │ │
│  └───────────────────────────────┘ │
└─────────────────────────────────────┘
         │ (Optional)
         ▼
┌─────────────────────────────────────┐
│  External Services (Opt-in)         │
│  - OpenAI API (embeddings)          │
│  - Future: team sync, backups       │
└─────────────────────────────────────┘
```

**Key Principles:**
1. **Data stays local by default** - No cloud dependencies for core functionality
2. **No telemetry** - We don't collect usage data
3. **Opt-in external services** - You control when data leaves your machine
4. **Transparent operation** - All data storage locations are documented

## Data Protection

### File System Security

**Storage Locations:**
```
~/.agent-memory/
├── *.db                      # SQLite databases (per-workspace)
├── agent-memory.env          # Environment variables
├── config.yaml               # User configuration
├── workspaces.json           # Workspace registry
├── models/                   # Embedding models
│   └── all-MiniLM-L6-v2/    # Local ONNX model
├── onnxruntime/              # ONNX Runtime libraries
├── dashboard/                # Dashboard assets
└── logs/                     # Application logs
```

**Recommended Permissions:**

```bash
# Set restrictive permissions
chmod 700 ~/.agent-memory
chmod 600 ~/.agent-memory/*.db
chmod 600 ~/.agent-memory/agent-memory.env
chmod 600 ~/.agent-memory/config.yaml
chmod 644 ~/.agent-memory/workspaces.json

# Verify permissions
ls -la ~/.agent-memory/
```

**Automated Setup:**
```bash
#!/bin/bash
# Add to your setup script
find ~/.agent-memory -type f -name "*.db" -exec chmod 600 {} \;
find ~/.agent-memory -type f -name "*.env" -exec chmod 600 {} \;
find ~/.agent-memory -type d -exec chmod 700 {} \;
```

### Database Security

**Encryption:**
- SQLite databases are **not encrypted by default**
- Encryption is planned for v2.0+
- For now, rely on OS-level security:
  - **macOS**: FileVault
  - **Windows**: BitLocker
  - **Linux**: LUKS, dm-crypt

**Access Control:**
- Each user has their own `~/.agent-memory/` directory
- OS user permissions enforce isolation
- No multi-user access within same directory (by design)

**Backup Security:**
```bash
# When backing up, maintain permissions
tar czf agent-memory-backup.tar.gz --preserve-permissions ~/.agent-memory

# Or use rsync with permissions
rsync -av --chmod=700 ~/.agent-memory /backup/location/
```

### Configuration Security

**Environment Variables:**
```bash
# Store secrets in environment variables, not in memories
export OPENAI_API_KEY=sk-...
export AGENT_MEMORY_ONNX_RUNTIME_PATH=/secure/path

# Never store secrets in:
# - agent-memory memories
# - config.yaml (unless encrypted)
# - git-tracked files
```

**Config File Security:**
```yaml
# ~/.agent-memory/config.yaml
# This file should have 600 permissions

# ❌ Don't store secrets here (unless using encrypted volume)
embeddings:
  openai_key: sk-...  # Bad!

# ✅ Reference environment variables
embeddings:
  provider: openai
  # openai_key loaded from $OPENAI_API_KEY
```

## Secret and PII Filtering

agent-memory automatically filters sensitive content during write operations.

### Automatic Detection

**Secret Patterns:**
```
API Keys:
- OpenAI: sk-[a-zA-Z0-9]{48}
- AWS: AKIA[0-9A-Z]{16}
- GitHub: gh[ps]_[a-zA-Z0-9]{36}
- Generic: [a-z0-9_-]*(api[_-]?key|token|secret)[a-z0-9_-]*\s*[:=]\s*[^\s]+

Private Keys:
- -----BEGIN (RSA|DSA|EC|OPENSSH) PRIVATE KEY-----

Credit Cards:
- Visa, MasterCard, Amex: \d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}

SSN:
- \d{3}-\d{2}-\d{4}

Email (when in sensitive contexts):
- [a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}
```

### Filtering Behavior

**Write Operation:**
```bash
# Original content
agent-memory write --type semantic \
  --content "API key is sk-abc123xyz...token123"

# Stored as (filtered)
# "API key is [FILTERED:API_KEY]...[FILTERED:TOKEN]"
```

**Search Results:**
```bash
# Filtered content is never returned
agent-memory search --query "api key"
# Results show [FILTERED:*] placeholders
```

### Manual Review

Periodically audit your memories:

```bash
# Search for potential secrets that might have been missed
agent-memory search --query "password" --top-k 20
agent-memory search --query "secret" --top-k 20
agent-memory search --query "token" --top-k 20

# Export and review manually
agent-memory export --format json > memories.json
grep -i "key\|password\|secret" memories.json
```

### Opt-Out (Not Recommended)

```bash
# Disable filtering (emergency only)
export AGENT_MEMORY_DISABLE_FILTERING=1

# This is NOT recommended for production use
# Only use for debugging or testing
```

## Network Security

### Dashboard HTTP Server

**Default Binding:**
```bash
# Secure (localhost only)
agent-memory dashboard --addr localhost:3042

# ❌ Insecure (accessible from network)
agent-memory dashboard --addr 0.0.0.0:3042  # Don't do this!
```

**Firewall Rules:**
```bash
# macOS
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add /path/to/agent-memory

# Linux (ufw)
sudo ufw deny 3042  # Block external access to dashboard port

# Linux (iptables)
sudo iptables -A INPUT -p tcp --dport 3042 -i lo -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 3042 -j DROP
```

**HTTPS (Future):**
- Dashboard currently uses HTTP (local only)
- HTTPS support planned for remote access scenarios (v2.0+)
- For now, rely on localhost-only binding

### External API Calls

**OpenAI Embeddings:**
```bash
# Data sent to OpenAI:
# - Memory content (for embedding generation)
# - No metadata or workspace info

# Control what gets sent:
export AGENT_MEMORY_EMBEDDING_PROVIDER=local  # Use local model (default)
# or
export AGENT_MEMORY_EMBEDDING_PROVIDER=openai  # Opt-in to cloud
```

**HTTP Client Security:**
- Uses Go's `net/http` with secure defaults
- TLS certificate validation enabled
- 30-second timeouts on all requests
- User-Agent: `agent-memory-installer/0.1`

## Development Security

### Secure Coding Practices

**Input Validation:**
```go
// ✅ Good: Validate and sanitize
func WriteMemory(content string) error {
    if len(content) > MaxContentSize {
        return ErrContentTooLarge
    }
    filtered := filterSecrets(content)
    return store.Write(filtered)
}

// ❌ Bad: No validation
func WriteMemory(content string) error {
    return store.Write(content)
}
```

**SQL Injection Prevention:**
```go
// ✅ Good: Parameterized queries
query := "SELECT * FROM memories WHERE content LIKE ?"
rows, err := db.Query(query, "%"+term+"%")

// ❌ Bad: String concatenation
query := "SELECT * FROM memories WHERE content LIKE '%" + term + "%'"
rows, err := db.Query(query)
```

**Path Traversal Prevention:**
```go
// ✅ Good: Clean and validate paths
func ReadWorkspace(name string) error {
    if !isValidWorkspaceName(name) {
        return ErrInvalidName
    }
    path := filepath.Join(dataDir, filepath.Clean(name)+".db")
    if !strings.HasPrefix(path, dataDir) {
        return ErrPathTraversal
    }
    return readDatabase(path)
}

// ❌ Bad: Direct path construction
func ReadWorkspace(name string) error {
    path := dataDir + "/" + name + ".db"
    return readDatabase(path)
}
```

**Error Messages:**
```go
// ✅ Good: User-friendly, no internals
return fmt.Errorf("failed to load workspace: %w", ErrNotFound)

// ❌ Bad: Exposes internal paths
return fmt.Errorf("failed to open /home/user/.agent-memory/secret.db: %v", err)
```

### Testing Security

**Security Test Suite:**
```go
func TestInputValidation(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr error
    }{
        {"valid", "normal content", nil},
        {"too large", strings.Repeat("a", 2_000_000), ErrTooLarge},
        {"path traversal", "../../../etc/passwd", ErrInvalid},
        {"null bytes", "content\x00evil", ErrInvalid},
    }
    // ...
}

func TestSecretFiltering(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"api key: sk-abc123", "api key: [FILTERED:API_KEY]"},
        {"password=secret123", "password=[FILTERED:PASSWORD]"},
    }
    // ...
}
```

**Run Security Tests:**
```bash
# Run all tests including security tests
go test ./... -v

# Run security-specific tests
go test ./internal/engine -run TestSecurity
go test ./internal/storage -run TestSecurity
```

## Deployment Security

### Production Checklist

- [ ] Use full-disk encryption (FileVault, BitLocker, LUKS)
- [ ] Set restrictive file permissions (700 for dirs, 600 for files)
- [ ] Use environment variables for secrets (not config files)
- [ ] Bind dashboard to localhost only
- [ ] Use local embedding provider for sensitive data
- [ ] Enable audit logging (future feature)
- [ ] Review memory content periodically
- [ ] Keep dependencies updated (`go mod tidy`, `go get -u`)
- [ ] Run security tests before deployment

### CI/CD Security

**GitHub Actions:**
```yaml
# .github/workflows/security.yml
name: Security Scan
on: [push, pull_request]
jobs:
  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      
      # Dependency scanning
      - name: Run govulncheck
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...
      
      # Static analysis
      - name: Run gosec
        run: |
          go install github.com/securego/gosec/v2/cmd/gosec@latest
          gosec ./...
      
      # Secret scanning
      - name: Run gitleaks
        uses: gitleaks/gitleaks-action@v2
```

### Container Security

**Dockerfile Best Practices:**
```dockerfile
# Use minimal base image
FROM golang:1.26-alpine AS builder

# Run as non-root
RUN adduser -D -u 1000 agent-memory
USER agent-memory

# Copy only necessary files
COPY --chown=agent-memory:agent-memory . /app

# Set secure environment
ENV AGENT_MEMORY_DATA_DIR=/data
VOLUME /data
```

## Threat Model

### Threat Actors

**1. Malicious Software on User's Machine**
- **Risk**: High (local-first means local access)
- **Mitigation**: OS-level security, antivirus, principle of least privilege

**2. Unauthorized Physical Access**
- **Risk**: Medium (depends on environment)
- **Mitigation**: Full-disk encryption, screen locks, secure workstations

**3. Network Attackers (External)**
- **Risk**: Low (localhost-only by default)
- **Mitigation**: Firewall rules, localhost binding, no remote access

**4. Supply Chain Attacks**
- **Risk**: Medium (dependency vulnerabilities)
- **Mitigation**: Dependency scanning, vendoring, regular updates

**5. Social Engineering**
- **Risk**: Medium (tricking users to expose data)
- **Mitigation**: User education, secret filtering, clear warnings

### Attack Vectors

**Local File Access:**
- **Attack**: Malware reads ~/.agent-memory/*.db
- **Defense**: File permissions, full-disk encryption, antivirus

**SQL Injection:**
- **Attack**: Malicious input exploits database queries
- **Defense**: Parameterized queries, input validation

**Path Traversal:**
- **Attack**: Crafted workspace name accesses arbitrary files
- **Defense**: Path validation, filepath.Clean, prefix checking

**Secret Leakage:**
- **Attack**: API keys stored in memories, exposed via search
- **Defense**: Automatic secret filtering, PII detection

**Dashboard Exposure:**
- **Attack**: Dashboard bound to 0.0.0.0, accessible from network
- **Defense**: Default localhost binding, firewall rules, warnings

### Risk Assessment

| Threat | Likelihood | Impact | Risk Level | Mitigation Status |
|--------|-----------|--------|------------|-------------------|
| Local malware | Medium | High | **High** | OS-level + monitoring |
| Physical access | Low | High | Medium | Disk encryption |
| Network attacks | Low | Medium | **Low** | Localhost binding |
| Supply chain | Medium | High | **Medium** | Dependency scanning |
| User error | High | Medium | **Medium** | Filtering + education |

## Incident Response

If you suspect a security incident:

1. **Isolate**: Stop agent-memory processes immediately
   ```bash
   pkill agent-memory
   agent-memory dashboard --stop
   ```

2. **Assess**: Check for unauthorized access
   ```bash
   # Check file modifications
   ls -lat ~/.agent-memory/
   
   # Check process list
   ps aux | grep agent-memory
   
   # Check network connections
   lsof -i | grep agent-memory
   ```

3. **Contain**: Remove compromised data if needed
   ```bash
   # Backup first
   tar czf agent-memory-incident-backup.tar.gz ~/.agent-memory/
   
   # Delete if compromised
   rm -rf ~/.agent-memory/*.db
   ```

4. **Report**: Follow responsible disclosure process (see SECURITY.md)

5. **Recover**: Restore from clean backup or reinitialize

## Additional Resources

- [OWASP Secure Coding Practices](https://owasp.org/www-project-secure-coding-practices-quick-reference-guide/)
- [CIS Benchmarks](https://www.cisecurity.org/cis-benchmarks/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
- [Go Security Best Practices](https://golang.org/doc/security/best-practices)

## Questions and Feedback

For security questions or suggestions:
- Open a GitHub Discussion with the "security" tag
- Email: [SECURITY_EMAIL_TO_BE_CONFIGURED]

Thank you for helping keep agent-memory secure!
