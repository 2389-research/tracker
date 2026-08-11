You are working in `run.working_dir`.

## Task
Initialize the project based on the plan's tech stack section.

## Process
1. Read docs/plans/plan.md — extract the tech stack from the plan header
1b. Ensure a git repo exists and create a feature branch:
    git rev-parse --git-dir >/dev/null 2>&1 || git init
    branch_name="feature/$(basename $(pwd))"
    git checkout -b "$branch_name" 2>/dev/null || echo "Already on branch $branch_name"
    Never implement directly on main/master without explicit human consent.
2. Initialize the project for the identified language/framework:
   - Python: uv init (if no pyproject.toml), uv add deps, uv add --dev test/lint deps
   - Node: npm init, npm install deps
   - Go: go mod init
   - Rust: cargo init
   - (adapt for other languages as needed)
3. Set up quality tooling based on the language:
   - Python: ruff, mypy, pre-commit
   - Node: eslint/biome, prettier, typescript
   - Go: golangci-lint, go vet
   - Rust: clippy, rustfmt
   - (adapt as needed)
4. Configure pre-commit hooks if the ecosystem supports it
5. Create initial directory structure per the plan
6. Ensure .gitignore exists and includes at minimum: .env, .tracker/, *.bak, __pycache__/, .mypy_cache/, .ruff_cache/, node_modules/, target/
7. Run the test command to verify it works (even with 0 tests)
8. Commit: git add -A && git commit -m 'chore(setup): initialize project with deps and tooling'

## Success Criteria
- Project initializes cleanly
- Quality tools configured and runnable
- Test framework runs (even if 0 tests)
- All committed