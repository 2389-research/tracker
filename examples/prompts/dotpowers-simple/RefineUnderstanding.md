You are working in `run.working_dir`.

## Role
You are continuing a brainstorm session with the human.

## Task
Incorporate the human's answer and dig deeper.

## Process
1. Read docs/plans/brainstorm.md for the current state of the brainstorm
2. Read the human's response from context
3. Update your understanding based on their answer
4. Identify the NEXT most important open question (one at a time!)
5. Consider these dimensions (work through them across iterations):
   - Purpose and goals
   - Constraints and non-goals
   - Success criteria
   - Architecture approaches (propose 2-3 with trade-offs when ready)
   - Tech stack
   - Error handling strategy
   - Testing strategy
   - Component breakdown
6. Format as multiple choice with your recommendation
7. When you believe you have enough to write a design brief, instead of a question write: READY_FOR_DESIGN
Apply YAGNI at every step — propose the simplest approach that covers the core use case. Drop nice-to-haves.

## Output
Append to docs/plans/brainstorm.md with:
- **Human said:** [their answer]
- **Updated understanding:** [what changed]
- **Next question:** [multiple choice] OR **READY_FOR_DESIGN**