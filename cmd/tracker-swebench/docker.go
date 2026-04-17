// ABOUTME: Docker container lifecycle management for the swebench benchmarking harness.
// ABOUTME: Shells out to the docker CLI to create, start, exec, stop, and remove containers per instance.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// AgentSummary holds token usage and timing stats extracted from agent-runner output.
type AgentSummary struct {
	Turns        int   `json:"turns"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}

// containerName returns the Docker container name for a given instance ID.
func containerName(instanceID string) string {
	return "swe-" + instanceID
}

// buildCloneCommands returns two safe argument slices: one for git clone, one
// for git checkout. No shell is involved — arguments are passed directly to
// exec, eliminating injection via dataset-controlled values.
// When cachePath is non-empty, --reference and --dissociate flags are added
// for local object reuse without creating fragile alternates dependencies.
func buildCloneCommands(repoURL, commit, workDir, cachePath string) (cloneArgs []string, checkoutArgs []string) {
	cloneArgs = []string{"git", "clone"}
	if cachePath != "" {
		cloneArgs = append(cloneArgs, "--reference", cachePath, "--dissociate")
	}
	cloneArgs = append(cloneArgs, repoURL, workDir)

	checkoutArgs = []string{"git", "-C", workDir, "checkout", commit}
	return cloneArgs, checkoutArgs
}

// writeEnvFile writes environment variables to a temporary file in KEY=VALUE
// format with mode 0600, returning the path. The caller must os.Remove the
// file when done. This avoids exposing secrets via docker -e flags which are
// visible in process listings and docker inspect output.
func writeEnvFile(env map[string]string) (string, error) {
	f, err := os.CreateTemp("", "swebench-env-*")
	if err != nil {
		return "", fmt.Errorf("create env file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("chmod env file: %w", err)
	}
	for k, v := range env {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", fmt.Errorf("write env var: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("close env file: %w", err)
	}
	return f.Name(), nil
}

// capturePatchCommands returns two argument slices: git add -A (to stage all
// changes including new files) and git diff HEAD (to produce a diff of all
// changes vs the original checkout commit).
func capturePatchCommands(workDir string) (addArgs []string, diffArgs []string) {
	addArgs = []string{"git", "-C", workDir, "add", "-A"}
	diffArgs = []string{"git", "-C", workDir, "diff", "HEAD"}
	return addArgs, diffArgs
}

// parseDiffOutput trims surrounding whitespace from raw git diff output.
func parseDiffOutput(raw string) string {
	return strings.TrimSpace(raw)
}

// patchLineCount counts non-empty lines in a patch string. Returns 0 for empty string.
func patchLineCount(patch string) int {
	if patch == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(patch, "\n") {
		if line != "" {
			count++
		}
	}
	return count
}

// parseAgentSummary extracts the AgentSummary JSON from the last non-empty line of output.
// Returns zero-value AgentSummary if the last line is not valid JSON.
func parseAgentSummary(output string) AgentSummary {
	lines := strings.Split(output, "\n")
	// Find last non-empty line.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var summary AgentSummary
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			return AgentSummary{}
		}
		return summary
	}
	return AgentSummary{}
}

// dockerCmd runs `docker <args>` and returns an error that includes stderr on failure.
func dockerCmd(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w\nstderr: %s", args[0], err, stderr.String())
	}
	return nil
}

// dockerExec runs `docker exec [--env-file <path>] <container> <args...>` and streams output to logs.
// Pass envFilePath="" when no environment variables are needed.
func dockerExec(ctx context.Context, container string, envFilePath string, args ...string) error {
	execArgs := []string{"exec"}
	if envFilePath != "" {
		execArgs = append(execArgs, "--env-file", envFilePath)
	}
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec %s: %w\nstderr: %s", container, err, stderr.String())
	}
	return nil
}

// dockerExecCapture runs docker exec and returns combined stdout+stderr as a string.
// Pass envFilePath="" when no environment variables are needed.
func dockerExecCapture(ctx context.Context, container string, envFilePath string, args ...string) (string, error) {
	execArgs := []string{"exec"}
	if envFilePath != "" {
		execArgs = append(execArgs, "--env-file", envFilePath)
	}
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("docker exec %s: %w\noutput: %s", container, err, out.String())
	}
	return out.String(), nil
}

// dockerExecOutput runs docker exec and returns stdout only (stderr is discarded).
func dockerExecOutput(ctx context.Context, container string, args ...string) (string, error) {
	execArgs := []string{"exec", container}
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker exec %s: %w\nstderr: %s", container, err, stderr.String())
	}
	return stdout.String(), nil
}

// DockerRunner manages the Docker container lifecycle for a single benchmark instance.
type DockerRunner struct {
	Image    string
	CacheDir string
	Timeout  time.Duration
}

// RunInstance creates a container, runs the agent, captures the diff patch, then cleans up.
// Returns the git diff patch and an AgentSummary. On agent timeout, returns a partial diff.
func (r *DockerRunner) RunInstance(ctx context.Context, inst Instance, agentEnv map[string]string) (patch string, summary AgentSummary, err error) {
	name := containerName(inst.InstanceID)
	const workDir = "/workspace"

	// Always stop and remove the container when done.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if stopErr := dockerCmd(stopCtx, "stop", name); stopErr != nil {
			log.Printf("[%s] docker stop: %v", inst.InstanceID, stopErr)
		}
		if rmErr := dockerCmd(stopCtx, "rm", "-f", name); rmErr != nil {
			log.Printf("[%s] docker rm: %v", inst.InstanceID, rmErr)
		}
	}()

	// Step 1: Create the container.
	createArgs := []string{"create", "--name", name}
	if r.CacheDir != "" {
		createArgs = append(createArgs, "-v", r.CacheDir+":/cache:ro")
	}
	createArgs = append(createArgs, r.Image, "sleep", "infinity")
	if err = dockerCmd(ctx, createArgs...); err != nil {
		return "", AgentSummary{}, fmt.Errorf("create container: %w", err)
	}

	// Step 2: Start the container.
	if err = dockerCmd(ctx, "start", name); err != nil {
		return "", AgentSummary{}, fmt.Errorf("start container: %w", err)
	}

	// Step 3: Clone the repo and checkout the base commit.
	cachePath := ""
	if r.CacheDir != "" {
		cachePath = "/cache/" + strings.ReplaceAll(inst.Repo, "/", "_") + ".git"
	}
	cloneArgs, checkoutArgs := buildCloneCommands(inst.RepoURL(), inst.BaseCommit, workDir, cachePath)
	if err = dockerExec(ctx, name, "", cloneArgs...); err != nil {
		return "", AgentSummary{}, fmt.Errorf("clone repo: %w", err)
	}
	if err = dockerExec(ctx, name, "", checkoutArgs...); err != nil {
		return "", AgentSummary{}, fmt.Errorf("checkout commit: %w", err)
	}

	// Step 4: Install the package (log failure but continue).
	pipOut, pipErr := dockerExecCapture(ctx, name, "", "sh", "-c", "pip install -e . 2>&1 | tail -5")
	if pipErr != nil {
		log.Printf("[%s] pip install failed (continuing): %v\noutput: %s", inst.InstanceID, pipErr, pipOut)
	} else {
		log.Printf("[%s] pip install: %s", inst.InstanceID, strings.TrimSpace(pipOut))
	}

	// Step 5: Run the agent with a timeout.
	// Write agent env to a secure temp file (avoids key exposure in process args).
	agentCtx, agentCancel := context.WithTimeout(ctx, r.Timeout)
	defer agentCancel()

	envFilePath, envErr := writeEnvFile(agentEnv)
	if envErr != nil {
		return "", AgentSummary{}, fmt.Errorf("write env file: %w", envErr)
	}
	defer os.Remove(envFilePath)

	agentOutput, agentErr := dockerExecCapture(agentCtx, name, envFilePath, "agent-runner")
	summary = parseAgentSummary(agentOutput)

	if agentErr != nil {
		log.Printf("[%s] agent-runner error: %v", inst.InstanceID, agentErr)
	}

	// Step 6: Stage all changes and capture diff vs HEAD (includes new files).
	// Use a fresh context so we still capture the patch even if parent ctx is cancelled.
	diffCtx, diffCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer diffCancel()

	addArgs, diffCmdArgs := capturePatchCommands(workDir)
	// Stage all changes (including untracked new files).
	if addErr := dockerExec(diffCtx, name, "", addArgs...); addErr != nil {
		log.Printf("[%s] git add -A failed: %v", inst.InstanceID, addErr)
	}
	diffOutput, diffErr := dockerExecOutput(diffCtx, name, diffCmdArgs...)
	if diffErr != nil {
		log.Printf("[%s] git diff HEAD failed: %v", inst.InstanceID, diffErr)
	}
	patch = parseDiffOutput(diffOutput)

	// Propagate agent error only after capturing the patch.
	if agentErr != nil {
		return patch, summary, fmt.Errorf("agent-runner: %w", agentErr)
	}

	return patch, summary, nil
}
