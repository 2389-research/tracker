Make one small change to the repository in the current directory.

The change: ${params.change}

If the change above is just the word `ask`, the user typed it at the prompt instead; it is:

${ctx.node.AskChange.human_response}

## If this is a revision

You may have made this change already and the user asked for something different. Their feedback, newest last (empty if this is the first attempt; a reviewer's choice word such as `revise` is not feedback):

${ctx.node.ReviewDiff.human_response}

${ctx.node.AskRevision.human_response}

Start from the files as they are now — your earlier attempt is still there — and adjust it.

## How to work

- The current directory is a separate git worktree on its own branch. The user's checkout is elsewhere and is not yours to touch: stay inside this directory.
- Make the **smallest** change that does what was asked. Don't refactor, reformat, rename or "improve" anything else, and don't add dependencies unless the change needs them.
- If there is a test suite and the change affects behavior, update or add a test the way the project already does.
- Do not commit, push, merge, rebase, switch branches or create tags — the workflow commits for you after the user approves. Never run `git push`.
- Don't read or write secrets (`.env`, keys, tokens).

## When you're done

Write a short summary to `.tracker/quickfix/summary.md` (create the directory if needed): two to five sentences on what you changed and why, naming the files. It becomes the commit message body, so write it for someone reading `git log`. Then stop.

If the request can't be done as a small change, change nothing, say why in the summary, and stop.
