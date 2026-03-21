# Ask and Execute Pipeline — Design Document

## Goal

An interactive pipeline that asks the user what they want via freeform input, then executes on that request using multi-model parallel implementation, validation, cross-model review, and conditional human approval when models disagree.

## Flow

```
Start
  -> Setup workspace
  -> Human Gate: "What would you like to do?" (freeform text)
  -> Interpret Request (Claude Opus): synthesize freeform into task spec
  -> Implement (Parallel): Claude Sonnet, GPT-5.2, Gemini Flash
  -> Implement Join
  -> Validate Build (tool node)
  -> Review (Parallel): Claude Opus, GPT-5.2, Gemini Flash
  -> Review Join
  -> Cross-Critiques (Parallel): 6 nodes, each model critiques the other two
  -> Critiques Join
  -> Review Analysis (goal gate): success / retry / disagree
    - success -> Commit -> Exit
    - retry -> loop back to Implement
    - disagree -> Human Approval Gate: approve / rework / reject
      - approve -> Commit -> Exit
      - rework -> loop back to Implement
      - reject -> Failure Summary -> Exit
```

## Design Decisions

### Freeform Capture

The first human gate captures open-ended text describing what the user wants. A single outgoing edge passes the response into an Interpret node that converts it into a structured, actionable task specification.

### Interpret Before Parallelize

A single Claude Opus node converts freeform input into a structured task before fanning out to parallel implementation. This ensures all three models work from the same understanding of the request.

### Conditional Human Gate

The ReviewAnalysis node routes to a human gate only on `outcome=disagree`. Clean model consensus goes straight to commit. This keeps the pipeline fast when models agree and safe when they don't.

### Validation Loop

Build/test failures loop back to Implement, matching the sprint_exec pattern.

### Model Assignment

| Role | Provider | Model |
|------|----------|-------|
| Interpret / Synthesize / Review Analysis | Anthropic | claude-opus-4-6 |
| Implement | Anthropic | claude-sonnet-4-6 |
| Implement + Review | OpenAI | gpt-5.2 |
| Implement + Review | Google | gemini-3-flash-preview |
| Review | Anthropic | claude-opus-4-6 |
