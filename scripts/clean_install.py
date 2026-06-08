#!/usr/bin/env python3
"""Remove redundant functions from install.go"""

# Ranges to remove (inclusive, 1-indexed)
REMOVE_RANGES = [
    (296, 682),   # ensureModel through firstExistingPath
    (683, 1060),  # ensureDashboard through copyDir (need to check actual end)
]

def should_keep_line(line_num, ranges):
    """Check if line should be kept"""
    for start, end in ranges:
        if start <= line_num <= end:
            return False
    return True

def clean_file(input_path, output_path):
    with open(input_path, 'r') as f:
        lines = f.readlines()
    
    kept_lines = []
    removed_count = 0
    
    for i, line in enumerate(lines, start=1):
        if should_keep_line(i, REMOVE_RANGES):
            kept_lines.append(line)
        else:
            removed_count += 1
    
    with open(output_path, 'w') as f:
        f.writelines(kept_lines)
    
    print(f"✓ Removed {removed_count} lines")
    print(f"✓ Kept {len(kept_lines)} lines")
    print(f"✓ Output: {output_path}")
    return removed_count, len(kept_lines)

if __name__ == "__main__":
    import sys
    input_file = sys.argv[1] if len(sys.argv) > 1 else "install.go"
    clean_file(input_file, input_file)
