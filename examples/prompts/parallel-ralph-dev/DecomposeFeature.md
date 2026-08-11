You are working in `run.working_dir`.

## Task
Decompose the feature into 2 parallel implementation streams with clear boundaries.

## Inputs
- `.ai/codebase-orientation.md` — codebase architecture
- `.ai/feature-spec.md` — feature requirements (if exists)
- Context from brainstorming (if it ran)

## Decomposition Rules
1. Prefer VERTICAL slices (end-to-end for a capability) over horizontal layers
2. Each stream must own DISTINCT files — no file can appear in 2 streams
3. Shared resources (DB schema, config, shared types) go in contracts, not streams
4. Each stream should be independently buildable and testable
5. Identify ALL cross-stream dependencies and put them in contracts

## Stream Count Heuristic
- 1 stream: change touches ≤3 files, no new API, no migrations
- 2 streams (default): new API + implementation, or UI + backend
- Write your reasoning for the stream count to `.ai/decomposition-rationale.md`

## Output — write ALL of these files:

### 1. Stream task files
For each stream, write `.ai/streams/stream-{a,b}/task.md` with:
```
# Stream {id} — {description}

## File Ownership
These are the ONLY files this stream may create or modify:
- path/to/file1.go
- path/to/file2.go
- path/to/file2_test.go

## Contracts (read-only)
- .ai/contracts/interfaces.go
- .ai/contracts/types.go

## Checklist
- [ ] Task 1 description
- [ ] Task 2 description
- [ ] Task 3 description
- [ ] Write tests for all new code
```

### 2. Contract files
Write `.ai/contracts/` with typed interfaces, shared types, error model:
- Interfaces/function signatures that cross stream boundaries
- Shared data types and enums
- Error types and error handling conventions
- 3-5 golden test cases (executable examples)

### 3. Dependency graph
Write `.ai/decomposition-deps.md` with:
- Inputs consumed by each stream
- Outputs produced by each stream
- Shared resources and their single owner
- Build/test order

## Self-Validation
Before finishing, verify:
- No file appears in 2+ stream ownership lists
- All cross-stream dependencies are in contracts
- Each stream has 3-8 checklist items (not too few, not too many)
- Contract files are syntactically valid for the project's language