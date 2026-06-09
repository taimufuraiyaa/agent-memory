#!/usr/bin/env python3
"""
Generate IDE-specific rule files from the single source template.
This ensures all IDE integrations stay in sync.
"""

import os
import sys
from pathlib import Path
from typing import Dict, List, Set

# IDE configurations: (target_file, target_ide_tags, header)
IDE_CONFIGS = [
    (
        ".cursor/rules/agent-memory.mdc",
        {"cursor"},
        "# agent-memory\nworkspace: agent-memory\n",
    ),
    (
        ".agents/rules/agent-memory.md",
        {"agents", "antigravity"},
        "# agent-memory\nworkspace: agent-memory\n",
    ),
    (
        ".trae/rules/project_rules.md",
        {"trae"},
        "## IDE / Codebase Agent Rules (ENFORCED)\n",
    ),
    (
        "CLAUDE.md",
        {"claude"},
        """# agent-memory — Project Instructions for AI Agents

## Rules (ENFORCED)
- Specs-first: follow `.kiro/specs/<feature>/requirements.md` → `design.md` → `tasks.md`.
- If no relevant spec exists for non-trivial work, create it before changing code.
- Mermaid: always quote node labels, e.g. `Node["Label (safe)"]`.
- Update spec task checkboxes in `.kiro/specs/**/tasks.md` when completing work.

---

""",
    ),
    (
        ".cursorrules",
        {"cursorrules"},
        "# AI Agent Rules - agent-memory\n",
    ),
    (
        ".aierules",
        {"aierules"},
        "# AI Agent Rules - agent-memory\n",
    ),
    (
        ".windsurfrules",
        {"windsurfrules"},
        "# AI Agent Rules - agent-memory\n",
    ),
]


def parse_template(template_path: Path) -> Dict[str, tuple]:
    """Parse template and return sections with their applies_to tags."""
    sections = {}
    lines = template_path.read_text().split("\n")
    
    i = 0
    while i < len(lines):
        line = lines[i].rstrip()
        
        # Look for section start delimiter
        if line == "---":
            i += 1
            if i >= len(lines):
                break
                
            # Parse metadata
            section_name = None
            applies_to = set()
            
            while i < len(lines) and lines[i].rstrip() != "---":
                meta_line = lines[i].strip()
                if meta_line.startswith("section: "):
                    section_name = meta_line[9:].strip()
                elif meta_line.startswith("applies_to: "):
                    tags_str = meta_line[12:].strip()
                    if tags_str.startswith("[") and tags_str.endswith("]"):
                        tags = tags_str[1:-1].split(",")
                        applies_to = {tag.strip() for tag in tags}
                i += 1
            
            # Skip the closing delimiter
            if i < len(lines) and lines[i].rstrip() == "---":
                i += 1
            
            # Collect content until next section or EOF
            content_lines = []
            while i < len(lines):
                if lines[i].rstrip() == "---":
                    # Start of next section
                    break
                content_lines.append(lines[i].rstrip())
                i += 1
            
            # Save section
            if section_name:
                # Remove leading/trailing empty lines from content
                while content_lines and not content_lines[0]:
                    content_lines.pop(0)
                while content_lines and not content_lines[-1]:
                    content_lines.pop()
                
                sections[section_name] = (applies_to, "\n".join(content_lines))
        else:
            i += 1
    
    return sections


def generate_file(
    project_root: Path,
    target_file: str,
    target_tags: Set[str],
    header: str,
    sections: Dict[str, tuple],
):
    """Generate a single IDE rule file."""
    output_path = project_root / target_file
    output_path.parent.mkdir(parents=True, exist_ok=True)

    with open(output_path, "w") as f:
        # Write header
        f.write(header)

        # Write applicable sections
        for section_name, (applies_to, content) in sections.items():
            # Check if any of the target tags match the section's applies_to
            if target_tags & applies_to:
                f.write("\n")
                f.write(content)
                f.write("\n")

    print(f"✓ Generated {target_file}")


def main():
    script_dir = Path(__file__).parent
    project_root = script_dir.parent
    template_path = project_root / ".kiro" / "templates" / "ide-rules-template.md"

    if not template_path.exists():
        print(f"❌ Error: Template not found at {template_path}", file=sys.stderr)
        sys.exit(1)

    print("ℹ Generating IDE rule files from template...\n")

    # Parse template
    sections = parse_template(template_path)
    print(f"ℹ Found {len(sections)} sections in template\n")

    # Generate each IDE file
    for target_file, target_tags, header in IDE_CONFIGS:
        generate_file(project_root, target_file, target_tags, header, sections)

    print("\n✓ All IDE rule files generated successfully!\n")
    print("ℹ Files generated:")
    for target_file, _, _ in IDE_CONFIGS:
        print(f"  - {target_file}")
    print("\nℹ To apply changes, commit the generated files")


if __name__ == "__main__":
    main()
