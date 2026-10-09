package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// maxLinkChain is the most symlinks FindDanglingLink follows from a dangling
// link to the missing target. It matches the usual kernel limit for nested
// symlinks, so a longer chain is reported as not found.
const maxLinkChain = 40

// DanglingLinkError reports a symlink on a write path whose target does not
// exist. The message names the link, the missing target, and the way to
// recover, so a caller that shows only the message still tells the user what
// to do.
type DanglingLinkError struct {
	// Link is the symlink path.
	Link string
	// Target is the link target, absolute: a relative target is joined to
	// filepath.Dir(Link).
	Target string
	// Remove is true for a file link that is safe to remove, such as a
	// preplan topic file. It is false for a folder link.
	Remove bool
	// Err is the original write error.
	Err error
}

// Error returns the link, the missing target, and the recovery text.
func (e *DanglingLinkError) Error() string {
	return fmt.Sprintf("%s is a link to %s, which does not exist. %s", e.Link, e.Target, e.Recovery())
}

// Recovery returns the text that tells the user how to fix the link. For a
// folder link (Remove false) it points at the session-start hook and the
// mkdir command that creates the missing target. For a file link (Remove
// true) it points at the rm command that removes the link.
func (e *DanglingLinkError) Recovery() string {
	if e.Remove {
		return fmt.Sprintf("Remove the link, then try again: rm %s", e.Link)
	}
	return fmt.Sprintf("Start a new session so the session-start hook repairs the link, or run: mkdir -p %s", e.Target)
}

// Unwrap returns the original write error, so errors.Is and errors.As still
// match it.
func (e *DanglingLinkError) Unwrap() error {
	return e.Err
}

// FindDanglingLink walks from path up to the root. For each element that
// exists (os.Lstat succeeds), a symlink whose os.Stat fails with
// fs.ErrNotExist is a hit; any other existing element stops the walk with
// ok=false. An element that does not exist is skipped and the walk goes on
// with its parent.
//
// On a hit, FindDanglingLink follows the chain with os.Readlink, at most
// maxLinkChain links, and returns the last link together with its target,
// which does not exist. A relative target is joined to the directory of the
// link that holds it. A longer chain, or a chain that ends in an existing
// element, gives ok=false.
func FindDanglingLink(path string) (link, target string, ok bool) {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink == 0 {
				return "", "", false
			}
			if _, statErr := os.Stat(p); !errors.Is(statErr, fs.ErrNotExist) {
				return "", "", false
			}
			return followLinkChain(p)
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", false
		}

		if parent := filepath.Dir(p); parent == p {
			return "", "", false
		}
	}
}

// followLinkChain follows the symlinks that start at link and returns the
// last link and its target, which does not exist. It gives ok=false when a
// link cannot be read, when the chain is longer than maxLinkChain, or when
// the chain ends in an element that exists.
func followLinkChain(link string) (string, string, bool) {
	for i := 0; i < maxLinkChain; i++ {
		next, err := os.Readlink(link)
		if err != nil {
			return "", "", false
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(link), next)
		}

		info, err := os.Lstat(next)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			link = next
		case errors.Is(err, fs.ErrNotExist):
			return link, next, true
		default:
			return "", "", false
		}
	}
	return "", "", false
}

// MkdirAll calls os.MkdirAll. When that fails with fs.ErrExist and
// FindDanglingLink(path) finds a link, it returns a *DanglingLinkError that
// wraps the os error. Otherwise it returns the os.MkdirAll error unchanged.
//
// The link check runs only after the write fails, so a path that works costs
// no extra file system calls.
func MkdirAll(path string, perm os.FileMode) error {
	err := os.MkdirAll(path, perm)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return err
	}

	link, target, ok := FindDanglingLink(path)
	if !ok {
		return err
	}
	return &DanglingLinkError{Link: link, Target: target, Err: err}
}
