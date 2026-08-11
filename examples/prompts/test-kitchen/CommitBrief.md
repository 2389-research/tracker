You are working in `run.working_dir`.

Stage and commit the design brief and brainstorm history:
git add docs/plans/design-brief.md docs/plans/brainstorm-context.md docs/plans/design-explore-opus.md docs/plans/design-explore-gpt.md docs/plans/design-explore-gemini.md
git commit -m 'docs(design): add project design brief (omakase)'

Also create a dated archive copy:
cp docs/plans/design-brief.md "docs/plans/$(date +%Y-%m-%d)-$(basename $(pwd))-design.md"
git add docs/plans/*-design.md
git commit --amend --no-edit

Verify: git log --oneline -1