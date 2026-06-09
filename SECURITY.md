# Security Policy

## Supported Versions

We release security updates for the following versions:

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 1.0   | :x:                |

**Note:** agent-memory is currently in active development (pre-1.0). Security updates are applied to the `main` branch. Once v1.0 is released, we will maintain security updates for stable releases.

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability, please report it responsibly.

### How to Report

**DO NOT** open a public GitHub issue for security vulnerabilities.

Instead, please email security reports to: **[SECURITY_EMAIL_TO_BE_CONFIGURED]**

Include in your report:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### What to Expect

1. **Acknowledgment**: We will acknowledge receipt within 48 hours
2. **Assessment**: We will assess the vulnerability and determine severity
3. **Communication**: We will keep you updated on our progress
4. **Resolution**: We will work on a fix and coordinate disclosure timing with you
5. **Credit**: We will credit you in our security advisory (unless you prefer to remain anonymous)

### Security Update Process

1. We will develop and test a fix
2. We will prepare a security advisory
3. We will coordinate disclosure with you
4. We will release the fix and publish the advisory
5. We will notify users through GitHub Security Advisories and release notes

## Security Best Practices

### For Users

#### Data Security

agent-memory is **local-first** by default:
- All data stored locally in `~/.agent-memory/`
- SQLite databases are unencrypted by default
- No data leaves your machine unless you explicitly configure external providers

**Recommendations:**
- Keep your `~/.agent-memory/` directory secure with appropriate file permissions
- Use full-disk encryption for sensitive data
- Review what content you store in memories (avoid secrets, passwords, API keys)
- Use `.gitignore` to prevent committing local agent-memory data

#### Secret and PII Filtering

agent-memory includes built-in filtering to prevent accidental storage of sensitive data:

**Automatic Detection:**
- API keys and tokens (common patterns)
- Credit card numbers
- Social security numbers
- Private keys (PEM format)
- Common environment variable patterns (`API_KEY`, `SECRET`, `TOKEN`, etc.)

**Content Filtering:**
```bash
# Secrets are automatically filtered during write
agent-memory write --type semantic --content "My API key is sk-..." 
# The API key pattern will be redacted to [FILTERED:API_KEY]
```

**Manual Review:**
```bash
# Always review what you're about to store
agent-memory search --query "password" --explain
```

**Best Practice:**
- Never manually override filtering
- Use environment variables for secrets (not stored in memories)
- Review memories periodically for sensitive data

### For Developers

#### Secure Coding Practices

**Input Validation:**
- All user input is validated before processing
- File paths are sanitized to prevent path traversal
- Workspace names are restricted to alphanumeric + dashes/underscores
- Content size limits are enforced

**Error Handling:**
- Use wrapped errors with `fmt.Errorf("context: %w", err)`
- Never expose internal paths or system details in user-facing errors
- Log security-relevant errors appropriately

**Database Security:**
- Use parameterized queries (we use SQLite with proper escaping)
- Validate all inputs before database operations
- Limit query complexity to prevent DoS

**Dependencies:**
- Keep dependencies up to date
- Review security advisories for dependencies
- Use `go mod tidy` regularly
- Pin dependency versions in production

#### Code Review Checklist

When reviewing code, check for:
- [ ] Input validation on all external inputs
- [ ] Proper error handling with context
- [ ] No hardcoded secrets or credentials
- [ ] SQL injection prevention (parameterized queries)
- [ ] Path traversal prevention (filepath.Clean)
- [ ] No sensitive data in logs
- [ ] Proper file permissions (0644 for files, 0755 for dirs)
- [ ] HTTPS for external requests
- [ ] Timeout on external requests

#### Testing Security

```bash
# Run security-focused tests
make test

# Check for common vulnerabilities
make vet

# Run full linting (includes security checks)
make lint
```

## Known Security Considerations

### Local Storage

**Issue:** SQLite databases in `~/.agent-memory/` are unencrypted

**Mitigation:**
- Use full-disk encryption (FileVault, BitLocker, LUKS)
- Set appropriate file permissions (600 for databases)
- Consider encrypted filesystems for sensitive projects

**Future:** We may add optional database encryption in v2.0+

### Embedding Providers

**Local Provider (default):**
- No data leaves your machine
- ONNX model runs locally
- No API keys required

**OpenAI Provider (optional):**
- Content is sent to OpenAI for embedding
- Subject to OpenAI's data handling policies
- Requires API key (store in environment variable, not in memories)
- Use with caution for sensitive codebases

**Recommendation:**
- Use local provider for sensitive projects
- Review OpenAI's data policy before using cloud embeddings
- Never send secrets/PII to external providers

### Dashboard Security

The local dashboard (HTTP server):
- Binds to `localhost` by default (not accessible externally)
- No authentication (relies on OS-level security)
- Serves static assets and API endpoints locally

**Recommendations:**
- Never expose dashboard to the internet
- Use `--addr localhost:PORT` (never `0.0.0.0`)
- Stop dashboard when not in use: `agent-memory dashboard --stop`

### Multi-User Systems

If multiple users share a machine:
- Each user should have their own `~/.agent-memory/` directory
- Use proper file permissions (700 for directories, 600 for files)
- Consider separate user accounts for sensitive work

## Security Advisories

Security advisories will be published at:
- GitHub Security Advisories: https://github.com/taimufuraiyaa/agent-memory/security/advisories
- Release Notes: https://github.com/taimufuraiyaa/agent-memory/releases

Subscribe to releases to receive notifications.

## Scope

### In Scope

- Vulnerabilities in agent-memory code
- Security issues in dependencies
- Data leakage or unauthorized access
- Authentication/authorization bypasses (when implemented)
- Code injection vulnerabilities
- Path traversal vulnerabilities

### Out of Scope

- Issues requiring physical access to the machine
- Social engineering attacks
- Third-party service vulnerabilities (OpenAI, external APIs)
- Misconfigurations by users
- Issues in forked/modified versions

## Security Features Roadmap

**V1.0:**
- ✅ Local-first architecture
- ✅ Secret/PII filtering
- ✅ Input validation
- ✅ Secure defaults
- ⏳ Security audit (planned)

**V2.0+ (Future):**
- Optional database encryption
- Multi-user access controls
- Audit logging
- Enhanced secret detection
- Team/shared memory security model

## Additional Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Go Security Best Practices](https://golang.org/doc/security/best-practices)
- [SQLite Security](https://www.sqlite.org/security.html)

## Questions?

For general security questions (not vulnerabilities), please:
- Open a GitHub Discussion
- Tag the question with "security"
- We'll respond within 1-2 business days

Thank you for helping keep agent-memory secure!
