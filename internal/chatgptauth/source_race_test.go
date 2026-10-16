//go:build !windows

// ABOUTME: Cross-process refresh race: this test binary re-runs itself as child processes that race
// ABOUTME: forceRefresh through the flock on one token file; plus the lock's wait-and-cancel contract.
package chatgptauth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/testutil"
)

// raceChildEnv switches this test binary into child mode (see TestMain).
// It is read only here, in a _test.go file, so it is no runtime setting.
const raceChildEnv = "TRACKER_CHATGPTAUTH_RACE_CHILD"

const (
	raceChildCount      = 2 // child processes the cross-process test starts: its whole fork count
	raceChildGoroutines = 4 // goroutines each child races through forceRefresh
	raceChildDeadline   = 30 * time.Second
)

// TestMain runs this binary as one child of
// TestTokenSourceCrossProcessRefreshOnce when raceChildEnv is "1", and as
// the ordinary test binary otherwise.
func TestMain(m *testing.M) {
	if os.Getenv(raceChildEnv) == "1" {
		os.Exit(runRaceChild(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runRaceChild is one child process of the cross-process race. Its
// arguments are the fake issuer's URL, the shared directory holding the
// token file, and this child's index. It creates ready-<index> once its
// goroutines wait on the start barrier, and opens the barrier when the
// parent creates "start". Each goroutine creates entered-<index>-<n> and
// calls forceRefresh for the rejected a-1; the child prints one line per
// goroutine — "refreshed" or "skipped" — and exits 1 if any call failed,
// with the error on stderr.
func runRaceChild(args []string) int {
	if len(args) != 3 {
		fmt.Fprintf(os.Stderr, "race child: want <issuer> <dir> <index>, got %q\n", args)
		return 2
	}
	issuer, dir, index := args[0], args[1], args[2]
	ctx, cancel := context.WithTimeout(context.Background(), raceChildDeadline)
	defer cancel()

	client := NewAuthClient()
	client.Issuer = issuer
	src := NewTokenSource(filepath.Join(dir, FileName), client)

	start := make(chan struct{})
	outcomes := make(chan string, raceChildGoroutines)
	for n := range raceChildGoroutines {
		go func() {
			<-start
			outcomes <- raceChildAttempt(ctx, src, filepath.Join(dir, fmt.Sprintf("entered-%s-%d", index, n)))
		}()
	}
	if err := os.WriteFile(filepath.Join(dir, "ready-"+index), nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "race child:", err)
		return 2
	}
	if err := waitForFile(ctx, filepath.Join(dir, "start")); err != nil {
		fmt.Fprintln(os.Stderr, "race child:", err)
		return 2
	}
	close(start)
	return printOutcomes(outcomes)
}

// raceChildAttempt marks this goroutine as in flight, then races
// forceRefresh, and names the outcome.
func raceChildAttempt(ctx context.Context, src *TokenSource, enteredMarker string) string {
	if err := os.WriteFile(enteredMarker, nil, 0o600); err != nil {
		return "error: " + err.Error()
	}
	refreshed, err := src.forceRefresh(ctx, "a-1")
	switch {
	case err != nil:
		return "error: " + err.Error()
	case refreshed:
		return "refreshed"
	default:
		return "skipped"
	}
}

// printOutcomes prints every goroutine's outcome — results to stdout,
// errors to stderr — and returns the child's exit code.
func printOutcomes(outcomes <-chan string) int {
	code := 0
	for range raceChildGoroutines {
		outcome := <-outcomes
		if strings.HasPrefix(outcome, "error: ") {
			fmt.Fprintln(os.Stderr, outcome)
			code = 1
			continue
		}
		fmt.Println(outcome)
	}
	return code
}

// waitForFile polls for path every millisecond until it exists or ctx ends.
func waitForFile(ctx context.Context, path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s never appeared: %w", filepath.Base(path), ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

// raceChild is one child process of the cross-process race and its output.
type raceChild struct {
	cmd            *exec.Cmd
	stdout, stderr bytes.Buffer
	exited         chan struct{} // closed once cmd.Wait returns
}

// startRaceChildren starts raceChildCount children of this test binary
// against the fake issuer and the shared dir. Cleanup kills any child
// still running.
func startRaceChildren(t *testing.T, issuer, dir string) []*raceChild {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	children := make([]*raceChild, 0, raceChildCount)
	t.Cleanup(func() { stopRaceChildren(children) })
	for i := range raceChildCount {
		c := &raceChild{exited: make(chan struct{})}
		c.cmd = exec.Command(exe, issuer, dir, strconv.Itoa(i))
		c.cmd.Env = append(os.Environ(), raceChildEnv+"=1")
		c.cmd.Stdout, c.cmd.Stderr = &c.stdout, &c.stderr
		if err := c.cmd.Start(); err != nil {
			t.Fatalf("start race child %d: %v", i, err)
		}
		go func() { _ = c.cmd.Wait(); close(c.exited) }()
		children = append(children, c)
	}
	return children
}

// stopRaceChildren kills every child still running and waits for all of
// them, so their exit codes and output are final.
func stopRaceChildren(children []*raceChild) {
	for _, c := range children {
		select {
		case <-c.exited:
		default:
			_ = c.cmd.Process.Kill()
		}
	}
	for _, c := range children {
		<-c.exited
	}
}

// raceReport renders every child's exit code, stdout and stderr. Call it
// only once every child has exited.
func raceReport(children []*raceChild) string {
	var b strings.Builder
	for i, c := range children {
		fmt.Fprintf(&b, "\n  child %d: exit=%d stdout=%q stderr=%q",
			i, c.cmd.ProcessState.ExitCode(), c.stdout.String(), c.stderr.String())
	}
	return b.String()
}

// tallyOutcomes counts the "refreshed" and "skipped" lines across the
// children's stdout.
func tallyOutcomes(children []*raceChild) (refreshed, skipped int) {
	for _, c := range children {
		lines := bufio.NewScanner(bytes.NewReader(c.stdout.Bytes()))
		for lines.Scan() {
			switch lines.Text() {
			case "refreshed":
				refreshed++
			case "skipped":
				skipped++
			}
		}
	}
	return refreshed, skipped
}

// countFiles counts the entries in dir matching the glob pattern.
func countFiles(dir, pattern string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(matches)
}

// anyExited reports whether a child has already exited.
func anyExited(children []*raceChild) bool {
	for _, c := range children {
		select {
		case <-c.exited:
			return true
		default:
		}
	}
	return false
}

// awaitChildren checks cond every millisecond until it holds, a child
// exits (which before the refresh is released means it failed), or
// deadline passes, and reports whether cond held; the caller renders its
// own failure.
func awaitChildren(t *testing.T, children []*raceChild, deadline time.Duration, cond func() bool) bool {
	t.Helper()
	testutil.Overlap(t, deadline, func() bool {
		if cond() || anyExited(children) {
			return true
		}
		time.Sleep(time.Millisecond)
		return false
	})
	return cond()
}

// TestTokenSourceCrossProcessRefreshOnce proves the cross-process lock: two
// tracker processes that share one token file and both hold a rejected
// token refresh it exactly once. The test starts exactly 2 child processes
// — this test binary re-run in child mode — and each races 4 goroutines
// through forceRefresh for the rejected a-1. The fake auth server rotates
// r-1 to r-2 and answers a spent r-1 with 400 refresh_token_reused, so a
// second refresh cannot pass unseen. It holds the one refresh until all 8
// goroutines are witnessed inside forceRefresh: the other 7 wait on their
// process's mutex or on the flock while that refresh is outstanding.
func TestTokenSourceCrossProcessRefreshOnce(t *testing.T) {
	srv := newFakeAuthServer(t)
	release := srv.holdRefreshes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := Save(path, signedInTokens("a-1", time.Now())); err != nil {
		t.Fatalf("Save: %v", err)
	}

	children := startRaceChildren(t, srv.URL(), dir)
	fail := func(format string, args ...any) {
		t.Helper()
		release()
		stopRaceChildren(children)
		t.Fatalf(format+"\nchildren:%s", append(args, raceReport(children))...)
	}

	if !awaitChildren(t, children, 20*time.Second, func() bool { return countFiles(dir, "ready-*") == raceChildCount }) {
		fail("only %d of %d children reached the start barrier", countFiles(dir, "ready-*"), raceChildCount)
	}
	if err := os.WriteFile(filepath.Join(dir, "start"), nil, 0o600); err != nil {
		fail("open the start barrier: %v", err)
	}
	const callers = raceChildCount * raceChildGoroutines
	if !awaitChildren(t, children, 20*time.Second, func() bool {
		return countFiles(dir, "entered-*") == callers && srv.refreshInFlightCount() >= 1
	}) {
		fail("race not witnessed: %d of %d goroutines entered forceRefresh, %d refresh(es) waiting at the server",
			countFiles(dir, "entered-*"), callers, srv.refreshInFlightCount())
	}
	release()

	deadline := time.After(raceChildDeadline)
	for i, c := range children {
		select {
		case <-c.exited:
		case <-deadline:
			fail("child %d still running %s after the refresh was released", i, raceChildDeadline)
		}
	}

	report := raceReport(children)
	for i, c := range children {
		if code := c.cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("child %d exited %d\nchildren:%s", i, code, report)
		}
	}
	refreshed, skipped := tallyOutcomes(children)
	if refreshed != 1 || skipped != callers-1 {
		t.Errorf("refreshed = %d, skipped = %d; want 1 and %d\nchildren:%s", refreshed, skipped, callers-1, report)
	}
	if calls, reused := srv.refreshCallCount(), srv.refreshReusedCount(); calls != 1 || reused != 0 {
		t.Errorf("refresh calls served = %d, refresh_token_reused answers = %d; want 1 and 0\nchildren:%s", calls, reused, report)
	}
	saved, err := Load(path)
	if err != nil || saved.AccessToken != "a-2" {
		t.Errorf("token file access token = %q (Load err %v), want a-2\nchildren:%s", saved.AccessToken, err, report)
	}
}

// TestRefreshLockWaitsForTheHolder pins lockFile's contract from both
// sides: while one holder has the lock, a second lockFile keeps waiting
// until its context ends; once the holder releases it, the next lockFile
// gets it. The lock file stays behind at 0600 — deleting it would let a
// newcomer lock a fresh file while an old holder still holds the old one.
func TestRefreshLockWaitsForTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	unlock, err := lockFile(context.Background(), path)
	if err != nil {
		t.Fatalf("first lockFile: %v", err)
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelWait()
	if _, err := lockFile(waitCtx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lockFile while the lock is held: err = %v, want context.DeadlineExceeded", err)
	}

	unlock()
	nextCtx, cancelNext := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelNext()
	unlockNext, err := lockFile(nextCtx, path)
	if err != nil {
		t.Fatalf("lockFile after the holder released it: %v", err)
	}
	unlockNext()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file after release: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("lock file mode = %04o, want 0600", perm)
	}
}

// TestSaveSignInWaitsForTheRefreshLock pins that a sign-in's save takes the
// same lock as a refresh, from both sides: while a refresh holds the lock,
// SaveSignIn waits until its context ends and writes nothing; once the
// refresh releases it, SaveSignIn saves. Without the lock, a refresh that
// loaded the file before the sign-in could save after it and put the old
// sign-in back.
func TestSaveSignInWaitsForTheRefreshLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	tokens := Tokens{
		IDToken:      "id-token",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-1",
		LastRefresh:  time.Unix(1760000000, 0).UTC(),
	}

	unlock, err := lockFile(context.Background(), filepath.Join(dir, LockFileName))
	if err != nil {
		t.Fatalf("take the refresh lock: %v", err)
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelWait()
	if err := SaveSignIn(waitCtx, path, tokens); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SaveSignIn while the refresh lock is held: err = %v, want context.DeadlineExceeded", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token file after SaveSignIn gave up waiting: stat err = %v, want not-exist", err)
	}

	unlock()
	nextCtx, cancelNext := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelNext()
	if err := SaveSignIn(nextCtx, path, tokens); err != nil {
		t.Fatalf("SaveSignIn after the refresh released the lock: %v", err)
	}
	if got, err := Load(path); err != nil || got.AccessToken != tokens.AccessToken {
		t.Fatalf("Load() after SaveSignIn = %+v, %v; want access token %q", got, err, tokens.AccessToken)
	}
}
