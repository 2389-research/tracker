Fix the Swift build errors shown above. You can read any file in Sources/OmniAgentsSDK/ to see the full source. Fix AS MANY files as possible in one pass.

Strategy:
1. Look at the error counts by file. Start with files that have the MOST errors.
2. Read each file, understand the errors, fix them.
3. Common issues: duplicate type declarations across files (keep the most complete one, remove the other), missing imports, function type labels (use _ not named params), type mismatches with OmniAICore, unterminated string literals, ambiguous type names (qualify with module prefix).
4. If a type is defined in multiple files, consolidate into one file and remove from others.
5. Write the fixed files using the Write tool.

Fix at least 5-10 files per pass. The goal is to reduce the error count significantly each iteration.