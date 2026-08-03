// ABOUTME: Hardened create for post-run capture files (spec artifacts, run.json).
// ABOUTME: Restrictive mode at creation + O_NOFOLLOW/symlink-refusal, matching the activity-log mirror (#213, #521, #529).
package pipeline

import "os"

// writeCaptureFile creates path with perm applied before any content lands and
// refuses to follow a symlink at the final component.
//
// The post-run capture files sit under .tracker/runs/<runID>/, inside a tool
// subprocess's cmd.Dir=workDir reach per the #213 threat model. Two hardening
// properties, matching the Close-time activity-log mirror:
//
//   - The mode is requested at creation (O_CREATE with perm), not chmod'd after
//     the write, so sensitive content never lands at a wider mode on the
//     fresh-file path (#521). A best-effort Chmod force-tightens a pre-existing
//     wider file that O_TRUNC reuses — the OpenFile perm only applies on create.
//     Same-UID access is the real gate; the mode is defense-in-depth against
//     other local users.
//   - O_NOFOLLOW (unix) refuses a symlink pre-planted at path, so a subprocess
//     cannot redirect the write to an outside target (#529). snapshotNoFollow is
//     0 on platforms lacking O_NOFOLLOW, matching the mirror's graceful
//     degradation there.
//
// The parent directory is the caller's responsibility to pre-flight (see
// refuseIfSymlink) — O_NOFOLLOW only guards the final component.
func writeCaptureFile(path string, data []byte, perm os.FileMode) error {
	if err := refuseIfSymlink(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|snapshotNoFollow, perm)
	if err != nil {
		return err
	}
	// Force-tighten: OpenFile's perm applies only on creation and is subject to
	// umask, so a pre-existing wider file (resumed run, reused dir) would keep
	// its mode. Best-effort — nothing sensitive is gained by failing the run
	// over a mode we could not narrow.
	_ = f.Chmod(perm)
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
