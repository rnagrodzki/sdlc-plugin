//go:build unix

package main

import (
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// TestDraftConflictFiles_NamedPipeRefused checks that the marker check refuses
// a named pipe in the draft with the stated error and leaves every draft file
// as it was. A named pipe is neither a regular file nor a folder.
func TestDraftConflictFiles_NamedPipeRefused(t *testing.T) {
	dir := t.TempDir()
	startWrite(t, dir, staticDir+"/f.txt", "clean\n")
	if err := syscall.Mkfifo(filepath.Join(dir, filepath.FromSlash(staticDir), "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := startSnapshotNoFollow(t, dir)
	want := staticDir + "/pipe is not a regular file. The draft may hold only files and folders."
	got, err := startConflictFiles(dir)
	if err == nil || err.Error() != want {
		t.Errorf("draftConflictFiles = %q, %v, want error %q", got, err, want)
	}
	if after := startSnapshotNoFollow(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("files changed\nbefore: %v\nafter:  %v", before, after)
	}
}
