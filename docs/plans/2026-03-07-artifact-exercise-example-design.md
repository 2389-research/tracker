# Artifact Exercise Example Design

**Goal:** Create a small example DOT pipeline that exercises both tool-node artifacts and codergen-node artifacts under the same `.tracker/runs/<run-id>/` tree.

**Approach:** Use a short sequential graph with one setup tool node, one codergen node that performs file operations through tools and emits a final text summary, and one verification tool node that prints the latest run artifact layout. This keeps the example easy to run and easy to inspect.

**Expected Behavior:**
- `SetupWorkspace` writes only `status.json` under the latest run directory.
- `CodergenWrite` writes `prompt.md`, `response.md`, and `status.json` under the same run directory.
- `VerifyArtifacts` writes its own `status.json` under that same run directory and prints a directory listing that proves the layout.
