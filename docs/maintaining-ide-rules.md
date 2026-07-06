# Maintaining IDE Rules

This document explains how to maintain the IDE-specific agent rules across different IDEs and editors.

## Overview

The agent-memory project supports multiple AI-powered IDEs and editors:
- Cursor (`.cursor/rules/agent-memory.mdc`)
- Antigravity/Agents (`.agents/rules/agent-memory.md`)
- Trae AI (`.trae/rules/project_rules.md`)
- Claude Desktop (`CLAUDE.md`)
- ZCode (`AGENTS.md`)
- Generic rule files (`.cursorrules`, `.aierules`, `.windsurfrules`)

To avoid maintaining duplicates, all these files are **generated** from a single source of truth.

## Single Source of Truth

**Location:** `.kiro/templates/ide-rules-template.md`

This template file contains:
1. All rule sections
2. Metadata indicating which IDEs each section applies to
3. Content that gets distributed to relevant IDE files

## Template Format

```markdown
---
section: section-name
applies_to: [ide1, ide2, ide3]
---

## Rule Content

Your rule content here...

---
section: another-section
applies_to: [ide4, ide5]
---

## More Content

More rules here...
```

### Section Metadata

- `section`: Unique identifier for the section
- `applies_to`: List of IDE tags this section should be included in

### Supported IDE Tags

- `cursor` - Cursor IDE (detailed memory policy)
- `agents` / `antigravity` - Antigravity/Agents IDE
- `trae` - Trae AI
- `claude` - Claude Desktop
- `zcode` - ZCode (`AGENTS.md`)
- `cursorrules` - Generic `.cursorrules` file
- `aierules` - Generic `.aierules` file
- `windsurfrules` - Generic `.windsurfrules` file

## Generating IDE Files

### Manual Generation

Run the generation script:

```bash
python3 scripts/generate-ide-rules.py
```

Or use the bash version:

```bash
./scripts/generate-ide-rules.sh
```

### Automatic Generation (Pre-commit Hook)

The pre-commit hook automatically regenerates IDE files when the template changes:

```bash
# Install pre-commit hooks
pip install pre-commit
pre-commit install

# Template changes will trigger automatic regeneration
git add .kiro/templates/ide-rules-template.md
git commit -m "Update IDE rules"
# Hook runs automatically, regenerates files, and includes them in commit
```

## Workflow

### Adding a New Rule

1. **Edit the template:**
   ```bash
   vim .kiro/templates/ide-rules-template.md
   ```

2. **Add your section:**
   ```markdown
   ---
   section: new-rule-name
   applies_to: [cursorrules, aierules, trae, claude]
   ---
   
   ## Your New Rule (ENFORCED)
   - Rule description
   - More details
   ```

3. **Regenerate all IDE files:**
   ```bash
   python3 scripts/generate-ide-rules.py
   ```

4. **Commit all changes:**
   ```bash
   git add .kiro/templates/ide-rules-template.md
   git add .cursor/rules/ .agents/rules/ .trae/rules/
   git add .cursorrules .aierules .windsurfrules CLAUDE.md AGENTS.md
   git commit -m "Add new IDE rule: [rule name]"
   ```

### Modifying an Existing Rule

1. Edit the section in `.kiro/templates/ide-rules-template.md`
2. Run `python3 scripts/generate-ide-rules.py`
3. Commit all modified files

### Adding Support for a New IDE

1. **Update the generation script:**
   Edit `scripts/generate-ide-rules.py` and add to `IDE_CONFIGS`:
   ```python
   (
       ".newide/rules/agent-memory.md",  # Target file
       {"newide"},                        # IDE tag(s)
       "# agent-memory\n",                # Header
   ),
   ```

2. **Add sections or update applies_to:**
   In `.kiro/templates/ide-rules-template.md`:
   ```markdown
   ---
   section: existing-section
   applies_to: [cursor, trae, newide]  # Add your IDE tag
   ---
   ```

3. **Regenerate and commit:**
   ```bash
   python3 scripts/generate-ide-rules.py
   git add scripts/generate-ide-rules.py .kiro/templates/ide-rules-template.md .newide/
   git commit -m "Add support for NewIDE"
   ```

## Verification

After regenerating, verify the changes:

```bash
# Check file counts
wc -l .cursor/rules/agent-memory.mdc .cursorrules CLAUDE.md

# Review generated content
head -30 .cursorrules
head -30 .cursor/rules/agent-memory.mdc

# Ensure no syntax errors
grep -r "FIXME\|TODO" .cursor/rules/ .agents/rules/ .trae/rules/
```

## Troubleshooting

### Files Not Updating

1. Check template syntax - ensure `---` delimiters are on their own lines
2. Verify `applies_to` tags match your target IDE
3. Run script manually with verbose output:
   ```bash
   python3 -v scripts/generate-ide-rules.py
   ```

### Merge Conflicts

If you have merge conflicts in generated files:

1. Accept the template changes (`.kiro/templates/ide-rules-template.md`)
2. Discard conflicts in generated files
3. Regenerate all files:
   ```bash
   python3 scripts/generate-ide-rules.py
   git add .
   ```

### Manual Edits Lost

**Never manually edit generated files!** Always edit the template:

```bash
# ❌ DON'T DO THIS
vim .cursorrules

# ✅ DO THIS INSTEAD
vim .kiro/templates/ide-rules-template.md
python3 scripts/generate-ide-rules.py
```

## Best Practices

1. **Always regenerate after template changes**
2. **Commit template and generated files together**
3. **Test rules in at least one IDE before committing**
4. **Keep sections focused and atomic**
5. **Use descriptive section names**
6. **Document why each rule exists (in comments)**

## CI/CD Integration

To ensure rules stay in sync, add to your CI pipeline:

```yaml
# .github/workflows/check-ide-rules.yml
name: Check IDE Rules

on: [pull_request]

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Generate IDE rules
        run: python3 scripts/generate-ide-rules.py
      - name: Check for differences
        run: |
          git diff --exit-code || \
            (echo "IDE rules out of sync! Run: python3 scripts/generate-ide-rules.py" && exit 1)
```

## Related Documentation

- [CONTRIBUTING.md](../CONTRIBUTING.md) - General contribution guidelines
- [.kiro/templates/](../.kiro/templates/) - Template directory
- [scripts/](../scripts/) - Generation scripts

---

**Last Updated:** 2026-06-08  
**Maintainer:** agent-memory team
