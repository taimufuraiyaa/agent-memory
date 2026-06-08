#!/usr/bin/env bash
# Audit error handling patterns in the codebase

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "=== Auditing Error Handling Patterns ==="
echo ""

echo "1. Simple 'return err' without context:"
echo "   (these should usually wrap with fmt.Errorf)"
echo ""
grep -rn "return err$" internal/ --include="*.go" | grep -v "_test.go" | head -20
echo ""

echo "2. Functions returning errors without fmt import:"
echo "   (needed for error wrapping)"
echo ""
for file in $(find internal/ -name "*.go" ! -name "*_test.go"); do
    if grep -q "return.*error" "$file" && ! grep -q "^import.*fmt" "$file" && ! grep -q "\"fmt\"" "$file"; then
        echo "  $file"
    fi
done | head -10
echo ""

echo "3. Error returns without %w (not wrapping):"
echo "   (should use %w to preserve error chain)"
echo ""
grep -rn 'fmt.Errorf.*%[^w]' internal/ --include="*.go" | grep -v "_test.go" | head -10
echo ""

echo "4. Count of error handling patterns:"
echo ""
echo "  return err (bare):              $(grep -r 'return err$' internal/ --include="*.go" | grep -v "_test.go" | wc -l | tr -d ' ')"
echo "  return fmt.Errorf with %w:      $(grep -r 'fmt.Errorf.*%w' internal/ --include="*.go" | grep -v "_test.go" | wc -l | tr -d ' ')"
echo "  return fmt.Errorf without %w:   $(grep -r 'fmt.Errorf' internal/ --include="*.go" | grep -v "_test.go" | grep -v '%w' | wc -l | tr -d ' ')"
echo ""

echo "5. Custom error types used:"
echo ""
grep -rn 'core\.\(New\|Err\)' internal/ --include="*.go" | grep -v "_test.go" | head -10
echo ""

echo "=== Audit Complete ==="
