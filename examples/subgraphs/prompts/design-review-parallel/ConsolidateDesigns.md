You are working in `run.working_dir`.

## Role
You are a senior architect synthesizing multiple independent design explorations into a single coherent design brief.

## Context
Three models independently explored design options. Read all three:
1. docs/plans/design-explore-opus.md
2. docs/plans/design-explore-gpt.md
3. docs/plans/design-explore-gemini.md

## Task
Compare all three explorations, cherry-pick the strongest ideas from each, resolve conflicts, and produce a single coherent design brief.

## Process
1. Read all three design exploration documents
2. Compare architectural approaches — which is simplest? Most robust? Best trade-offs?
3. Compare tech stack choices — where do they agree? Where do they diverge?
4. Compare component breakdowns — which is cleanest?
5. Compare testing strategies — which is most comprehensive?
6. Cherry-pick the best ideas from each, resolving conflicts
7. Apply YAGNI — if two models wanted a feature and one didn't, lean toward dropping it

## Output
Write to docs/plans/design-brief.md with sections:
1. **Architecture and Components** — components, data flow, key decisions
2. **Tech Stack** — language, framework, key dependencies. Justify each choice.
3. **Error Handling** — error categories, handling strategies, recovery patterns
4. **Testing Strategy** — test framework, test levels, coverage targets
5. **Component Breakdown** — detailed specs with dependency graph

Note which ideas came from which model's exploration, giving credit where due.