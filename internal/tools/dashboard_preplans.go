package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

const (
	// dashboardPreplanLimit is the number of topic files the list keeps,
	// newest first.
	dashboardPreplanLimit = 100
	// dashboardPreplanHeadMax is the number of bytes read from the start of
	// each topic file. The topic line and the status line sit at the top.
	dashboardPreplanHeadMax = 4 << 10
)

// dashboardPreplanTopicRe matches the "# Preplan: <topic>" heading line of a
// topic file, capturing the topic text.
var dashboardPreplanTopicRe = regexp.MustCompile(`(?m)^# Preplan:[ \t]*(.*)$`)

// dashboardPreplanStatusRe matches the "**Status:** <status>" line of a topic
// file, capturing the status text.
var dashboardPreplanStatusRe = regexp.MustCompile(`(?m)^\*\*Status:\*\*[ \t]*(.*)$`)

// dashboardPreplanFile is one topic file that passed the Stat check.
type dashboardPreplanFile struct {
	slug string
	path string // absolute path of the file
	mod  time.Time
}

// dashboardPreplans lists the preplan topic files of root, newest first and
// ties by slug, so the snapshot hash stays the same between two polls. A read
// problem never fails the list: it adds one text to warnings and the rest of
// the list stays right. A missing preplan folder (or a dangling link in its
// place) is not a problem: it gives an empty list and no warning.
//
// list is never nil. The text of a topic and of a status goes through
// dashboardPreview, like the text of a timeline event.
func dashboardPreplans(root string) (list []DashboardPreplan, warnings []string) {
	list = []DashboardPreplan{}
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("Preplan folder not read: %v", err))
		}
		return list, warnings
	}

	var files []dashboardPreplanFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || name == ".md" {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		full := filepath.Join(dir, name)
		// Stat follows a link, so a link to a file counts as that file and a
		// dangling link gives the file error. A file that is gone since
		// ReadDir (a delete during the snapshot) is skipped with no warning.
		fi, err := os.Stat(full)
		if err != nil {
			if !dashboardPreplanGone(full, err) {
				warnings = append(warnings, fmt.Sprintf("Preplan %s not read: %v", slug, err))
			}
			continue
		}
		// A sub-folder is skipped. So is a pipe or a device: opening one
		// could block the snapshot.
		if !fi.Mode().IsRegular() {
			continue
		}
		files = append(files, dashboardPreplanFile{slug: slug, path: full, mod: fi.ModTime()})
	}

	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].slug < files[j].slug
	})
	var older int
	if len(files) > dashboardPreplanLimit {
		older = len(files) - dashboardPreplanLimit
		files = files[:dashboardPreplanLimit]
	}

	for _, f := range files {
		head, err := dashboardPreplanHead(f.path)
		if err != nil {
			if !dashboardPreplanGone(f.path, err) {
				warnings = append(warnings, fmt.Sprintf("Preplan %s not read: %v", f.slug, err))
			}
			continue
		}
		topic := f.slug
		if m := dashboardPreplanTopicRe.FindStringSubmatch(head); m != nil {
			if t := dashboardPreview(strings.TrimSpace(m[1])); t != "" {
				topic = t
			}
		}
		status := ""
		if m := dashboardPreplanStatusRe.FindStringSubmatch(head); m != nil {
			status = dashboardPreview(strings.TrimSpace(m[1]))
		}
		list = append(list, DashboardPreplan{
			Slug:      f.slug,
			Topic:     topic,
			Status:    status,
			Path:      path.Join(paths.DataDir, paths.PreplanSubdir, f.slug+".md"),
			UpdatedAt: dashboardFormatTime(f.mod),
		})
	}
	if older > 0 {
		warnings = append(warnings, fmt.Sprintf("%d older preplan topic files not shown", older))
	}
	return list, warnings
}

// dashboardPreplanGone reports whether err, from a Stat or an open of the
// topic file at p, means that the file itself is gone: err is
// fs.ErrNotExist and an Lstat of p finds nothing either. A dangling link
// gives false: its Lstat finds the link, so it keeps its warning.
func dashboardPreplanGone(p string, err error) bool {
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, lerr := os.Lstat(p)
	return errors.Is(lerr, fs.ErrNotExist)
}

// dashboardPreplanHead returns the first dashboardPreplanHeadMax bytes of the
// file at p as valid UTF-8 text. A cut inside a multi-byte character drops the
// broken bytes.
func dashboardPreplanHead(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, dashboardPreplanHeadMax))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(buf), ""), nil
}
