Answer a question about the repository in the current directory.

The question: ${params.question}

If the question above is just the word `ask`, the user typed their question at the prompt instead; it is:

${ctx.node.AskQuestion.human_response}

You can read files, search them, and list them. You cannot change anything, and you don't need to.

How to work:

- Start from the top: the README, the build or package manifest, the main entry points. Go deeper only as far as the question needs.
- Respect `.gitignore`. Skip vendored and generated trees (`vendor/`, `node_modules/`, `third_party/`, `dist/`, `build/`, `.git/`, lockfiles) unless the question is about them.
- Don't try to read secrets (`.env`, private keys); those reads are refused.

How to answer:

- Lead with the direct answer in one or two sentences, then the supporting detail.
- Name the files (with paths) that the answer rests on, so the user can check it.
- Be concise: a few short paragraphs or a short list. No preamble, no summary of what you did.
- If the repo doesn't contain enough to answer, say what is missing rather than guessing.
