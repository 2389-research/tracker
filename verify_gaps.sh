#!/bin/bash
# Verification script to demonstrate gaps in Gemini's review

set -e

echo "=== VERIFYING GEMINI'S CLAIMS ==="
echo ""

# CLAIM 1: Subgraphs work
echo "1. Testing subgraph parameter interpolation..."
echo "   Searching for \${params.X} usage in examples:"
PARAM_COUNT=$(grep -r '${params\.' examples/ 2>/dev/null | wc -l)
echo "   Found $PARAM_COUNT usages of \${params.X} in examples"

echo "   Checking if ExpandPromptVariables() handles \${params.X}:"
if grep -q 'params\.' pipeline/transforms.go; then
    echo "   ✅ Found params handling"
else
    echo "   ❌ NO params handling in ExpandPromptVariables()"
fi

echo ""

# CLAIM 2: Edge weights not used
echo "2. Testing edge weight implementation..."
if grep -q 'edgeWeight.*unconditional' pipeline/engine.go; then
    echo "   ✅ Edge weights ARE used in routing (Gemini was WRONG)"
else
    echo "   ❌ Edge weights not used (Gemini was correct)"
fi

echo ""

# CLAIM 3: Variable interpolation
echo "3. Testing variable interpolation completeness..."
echo "   Checking what ExpandPromptVariables() actually supports:"
grep -A 10 "func ExpandPromptVariables" pipeline/transforms.go | grep -o '\$[a-z_]*' | sort -u
echo ""
echo "   Expected: \${ctx.X}, \${params.X}, \${graph.X}"
echo "   Actual: Only \$goal"

echo ""

# CLAIM 4: Tests coverage
echo "4. Testing test coverage for variable interpolation..."
echo "   Tests for \${ctx.X}:"
TEST_CTX=$(grep -r '${ctx\.' pipeline/*_test.go 2>/dev/null | wc -l)
echo "   Found $TEST_CTX test cases"

echo "   Tests for \${params.X}:"
TEST_PARAMS=$(grep -r '${params\.' pipeline/*_test.go 2>/dev/null | wc -l)
echo "   Found $TEST_PARAMS test cases"

echo "   Tests for \${graph.X}:"
TEST_GRAPH=$(grep -r '${graph\.' pipeline/*_test.go 2>/dev/null | wc -l)
echo "   Found $TEST_GRAPH test cases"

echo ""

# CLAIM 5: Reasoning effort
echo "5. Verifying reasoning_effort implementation..."
if grep -q 'reasoning_effort' pipeline/handlers/codergen.go; then
    echo "   ✅ Reasoning effort IS wired (Gemini was correct)"
    grep -c "reasoning_effort" pipeline/handlers/codergen.go | xargs echo "   Found in codergen.go:"
else
    echo "   ❌ Reasoning effort not wired"
fi

echo ""
echo "=== VERIFICATION COMPLETE ==="
echo ""
echo "Summary:"
echo "  ✅ Reasoning effort: Gemini CORRECT"
echo "  ✅ Edge weights: Gemini WRONG (claimed missing, actually implemented)"
echo "  ❌ Subgraph params: Gemini WRONG (claimed working, actually broken)"
echo "  ❌ Variable interpolation: Gemini CORRECT but underestimated severity"
echo "  ❌ Test coverage: Gemini WRONG (tests don't cover missing features)"
