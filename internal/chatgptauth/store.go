// ABOUTME: The ChatGPT token file: default path, atomic save, and a Load that refuses a
// ABOUTME: missing, incomplete, symlinked or over-permissive file before touching its contents.
package chatgptauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2389-research/tracker/internal/xdgconfig"
)

// Tokens is the ChatGPT OAuth token set persisted to the token file.
type Tokens struct {
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	LastRefresh  time.Time `json:"last_refresh"`
}

// FileName is the token file's name under its directory (see DefaultPath).
const FileName = "chatgpt-auth.json"

// LockFileName is the cross-process refresh lock's name, kept next to the
// token file. This package only reserves the name here; taking the flock
// itself is a later task (design §1 "Refresh").
const LockFileName = "chatgpt-auth.lock"

// ErrNotSignedIn is returned by Load, and folded into ReadStatus, when the
// token file does not exist.
var ErrNotSignedIn = errors.New("not signed in to ChatGPT; run `tracker setup` and choose OpenAI → ChatGPT subscription")

// UnsafeFileError reports that Load refused to read the token file because
// its symlink status or permission bits could expose it to another account
// on the same machine.
type UnsafeFileError struct{ Path, Problem string }

// Error picks its fix hint by the kind of Problem: a symlink is fixed by
// replacing it with a regular file, loose mode bits by chmod.
func (e *UnsafeFileError) Error() string {
	if strings.Contains(e.Problem, "symlink") {
		return fmt.Sprintf("refusing %s: %s; replace the symlink with a regular file", e.Path, e.Problem)
	}
	return fmt.Sprintf("refusing %s: %s; fix with: chmod 600 %s", e.Path, e.Problem, e.Path)
}

// DefaultPath returns the token file's default location:
// $XDG_CONFIG_HOME/tracker/chatgpt-auth.json, falling back to
// $HOME/.config/tracker/... when XDG_CONFIG_HOME is unset or relative — see
// internal/xdgconfig.ConfigHome.
func DefaultPath() (string, error) {
	dir, _, err := xdgconfig.ConfigHome()
	if err != nil {
		return "", fmt.Errorf("resolve config home: %w", err)
	}
	return filepath.Join(dir, "tracker", FileName), nil
}

// Load reads and validates the token file at path.
//
// A missing file is ErrNotSignedIn. A symlink, or (on Unix) a mode with any
// group or other bit set, is a *UnsafeFileError — checkFileSafe (store_unix.go
// / store_windows.go) decides this before the content is ever read. A file
// that parses but is missing its access token, refresh token or account ID
// is an incomplete sign-in: the error names the problem but never repeats
// any token value.
func Load(path string) (Tokens, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Tokens{}, ErrNotSignedIn
		}
		return Tokens{}, fmt.Errorf("stat token file: %w", err)
	}
	if err := checkFileSafe(path, info); err != nil {
		return Tokens{}, err
	}

	f, err := openNoFollow(path)
	if err != nil {
		return Tokens{}, fmt.Errorf("open token file: %w", err)
	}
	defer f.Close()

	var t Tokens
	if err := json.NewDecoder(f).Decode(&t); err != nil {
		return Tokens{}, fmt.Errorf("parse token file: %w", err)
	}
	if t.incomplete() {
		return Tokens{}, errors.New("chatgpt sign-in is incomplete; run `tracker setup` to sign in again")
	}
	return t, nil
}

// incomplete reports whether t is missing a field a usable sign-in requires:
// the access token, the refresh token, or the account ID.
func (t Tokens) incomplete() bool {
	return t.AccessToken == "" || t.RefreshToken == "" || t.AccountID == ""
}

// Save writes t to path atomically: a temp file at mode 0600 in the same
// directory, fsynced, then renamed over path. MkdirAll creates a missing
// directory at 0700; it leaves an already-existing directory's mode exactly
// as found (MkdirAll is a no-op on an existing directory — it never chmods).
func Save(path string, t Tokens) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}

	if err := saveAtomic(path, data); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

// saveAtomic writes data to a temp file beside path at mode 0600, fsyncs it,
// then renames it over path. The rename is atomic on POSIX same-directory, so
// a reader always sees either the old-complete or the new-complete file,
// never a partial one; replacing path this way also never follows a symlink
// planted at path itself. Unlike pipeline's checkpoint writeFileAtomic, this
// fsyncs before the rename: a lost refresh token forces a fresh sign-in, so
// the extra durability is worth the latency here.
func saveAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600) // O_CREATE's perm is subject to umask; force-tighten (#529 pattern)
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
