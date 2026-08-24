#!/usr/bin/env bash
set -euo pipefail

# Generate IDE-specific rule files from the single source template
# This ensures all IDE integrations stay in sync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE="$PROJECT_ROOT/.kiro/templates/ide-rules-template.md"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${BLUE}ℹ${NC} $*"
}

success() {
    echo -e "${GREEN}✓${NC} $*"
}

warn() {
    echo -e "${YELLOW}⚠${NC} $*"
}

generate_file() {
    local target_ide="$1"
    local target_file="$2"
    local header="$3"
    
    info "Generating $target_file for $target_ide..."
    
    local output_dir="$(dirname "$target_file")"
    mkdir -p "$output_dir"
    
    {
        # Add file header
        echo "$header"
        echo ""
        
        # Parse template and extract relevant sections
        local in_section=false
        local section_applies=false
        local section_name=""
        
        while IFS= read -r line; do
            # Check for section delimiter
            if [[ "$line" == "---" ]]; then
                if [[ "$in_section" == true ]]; then
                    in_section=false
                    section_applies=false
                    section_name=""
                else
                    in_section=true
                fi
                continue
            fi
            
            # Parse section metadata
            if [[ "$in_section" == true ]]; then
                if [[ "$line" =~ ^section:\ (.+)$ ]]; then
                    section_name="${BASH_REMATCH[1]}"
                elif [[ "$line" =~ ^applies_to:\ \[(.+)\]$ ]]; then
                    local applies_list="${BASH_REMATCH[1]}"
                    if [[ ",$applies_list," == *",$target_ide,"* ]]; then
                        section_applies=true
                    fi
                fi
                continue
            fi
            
            # Output section content if it applies
            if [[ "$section_applies" == true ]]; then
                echo "$line"
            fi
        done < "$TEMPLATE"
    } > "$target_file"
    
    success "Generated $target_file"
}

# Main generation logic
main() {
    cd "$PROJECT_ROOT"
    
    info "Generating IDE rule files from template..."
    echo ""
    
    # Cursor (.cursor/rules/agent-memory.mdc)
    generate_file "cursor" \
        ".cursor/rules/agent-memory.mdc" \
        "---
description: Always use agent-memory CLI for memory search, recall, write, and session-end
globs: *
alwaysApply: true
---
# agent-memory
workspace: agent-memory"
    
    # Antigravity (.agents/rules/agent-memory.md)
    generate_file "antigravity" \
        ".agents/rules/agent-memory.md" \
        "---
trigger: always_on
---
# agent-memory
workspace: agent-memory"
    
    # Trae (.trae/rules/project_rules.md)
    generate_file "trae" \
        ".trae/rules/project_rules.md" \
        "## IDE / Codebase Agent Rules (ENFORCED)"
    
    # Claude (CLAUDE.md)
    generate_file "claude" \
        "CLAUDE.md" \
        "# agent-memory — Project Instructions for AI Agents

## Rules (ENFORCED)
- Specs-first: follow \`.kiro/specs/<feature>/requirements.md\` → \`design.md\` → \`tasks.md\`.
- If no relevant spec exists for non-trivial work, create it before changing code.
- Mermaid: always quote node labels, e.g. \`Node[\"Label (safe)\"]\`.
- Update spec task checkboxes in \`.kiro/specs/**/tasks.md\` when completing work.

---"
    
    # Root-level rule files (.cursorrules, .aierules, .windsurfrules)
    generate_file "cursorrules" \
        ".cursorrules" \
        "# AI Agent Rules - agent-memory"
    
    generate_file "aierules" \
        ".aierules" \
        "# AI Agent Rules - agent-memory"
    
    generate_file "windsurfrules" \
        ".windsurfrules" \
        "# AI Agent Rules - agent-memory"

    # ZCode (AGENTS.md)
    generate_file "zcode" \
        "AGENTS.md" \
        "# AI Agent Rules - agent-memory"

    echo ""
    success "All IDE rule files generated successfully!"
    echo ""
    info "Files generated:"
    echo "  - .cursor/rules/agent-memory.mdc"
    echo "  - .agents/rules/agent-memory.md"
    echo "  - .trae/rules/project_rules.md"
    echo "  - CLAUDE.md"
    echo "  - .cursorrules"
    echo "  - .aierules"
    echo "  - .windsurfrules"
    echo "  - AGENTS.md"
    echo ""
    info "To apply changes, commit the generated files"
}

# Check if template exists
if [[ ! -f "$TEMPLATE" ]]; then
    echo "Error: Template not found at $TEMPLATE"
    exit 1
fi

main "$@"
