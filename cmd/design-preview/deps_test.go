package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// depsWriteFile writes content to a new file in a temp folder and returns its path.
func depsWriteFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dependencies.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	return path
}

func TestLoadDependencies(t *testing.T) {
	t.Run("valid file returns records in file order", func(t *testing.T) {
		path := depsWriteFile(t, `[
  { "id": "D2", "kind": "state-write", "need": "Archive button writes the run state.", "elements": ["#archive"] },
  { "id": "D1", "kind": "data", "need": "Snapshot gives the queue wait time for each run.",
    "elements": ["#run-row .wait", ".wait-total"], "sample": "4m 12s" },
  { "id": "D10", "kind": "flow", "need": "Opening a run shows its detail page.", "elements": [], "sample": {"a": [1, 2]} }
]`)

		got, err := LoadDependencies(path)
		if err != nil {
			t.Fatalf("LoadDependencies: %v", err)
		}

		want := []Dependency{
			{ID: "D2", Kind: "state-write", Need: "Archive button writes the run state.", Elements: []string{"#archive"}},
			{ID: "D1", Kind: "data", Need: "Snapshot gives the queue wait time for each run.",
				Elements: []string{"#run-row .wait", ".wait-total"}, Sample: []byte(`"4m 12s"`)},
			{ID: "D10", Kind: "flow", Need: "Opening a run shows its detail page.", Elements: []string{}, Sample: []byte(`{"a": [1, 2]}`)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("records differ\n got: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("empty array returns an empty slice", func(t *testing.T) {
		got, err := LoadDependencies(depsWriteFile(t, "  []\n"))
		if err != nil {
			t.Fatalf("LoadDependencies: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got %#v, want an empty non-nil slice", got)
		}
	})

	t.Run("omitted and null elements become an empty slice", func(t *testing.T) {
		path := depsWriteFile(t, `[
  { "id": "D1", "kind": "data", "need": "No elements field." },
  { "id": "D2", "kind": "data", "need": "Null elements field.", "elements": null }
]`)

		got, err := LoadDependencies(path)
		if err != nil {
			t.Fatalf("LoadDependencies: %v", err)
		}
		for _, d := range got {
			if d.Elements == nil || len(d.Elements) != 0 {
				t.Errorf("%s: Elements = %#v, want an empty non-nil slice", d.ID, d.Elements)
			}
		}
	})

	// Each case writes the file, then compares the error text with want.
	// The placeholder PATH in want stands for the path of the fixture file.
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "invalid syntax",
			content: `[{"id": "D1",`,
			want:    "PATH: not valid JSON: unexpected end of JSON input",
		},
		{
			name:    "empty file",
			content: ``,
			want:    "PATH: not valid JSON: unexpected end of JSON input",
		},
		{
			name:    "unknown field",
			content: `[{"id": "D1", "kind": "data", "need": "x", "status": "open"}]`,
			want:    `PATH: not valid JSON: json: unknown field "status"`,
		},
		{
			name:    "text after the array",
			content: `[] []`,
			want:    "PATH: not valid JSON: invalid character '[' after top-level value",
		},
		{
			name:    "root is null",
			content: `null`,
			want:    "PATH: root must be a JSON array",
		},
		{
			name:    "root is an object",
			content: ` {"id": "D1"}`,
			want:    "PATH: root must be a JSON array",
		},
		{
			name:    "root is a string",
			content: `"D1"`,
			want:    "PATH: root must be a JSON array",
		},
		{
			name: "duplicate id",
			content: `[
  {"id": "D1", "kind": "data", "need": "First."},
  {"id": "D2", "kind": "data", "need": "Second."},
  {"id": "D1", "kind": "flow", "need": "Third."}
]`,
			want: "PATH: record 3: duplicate id D1",
		},
		{
			name:    "id without number",
			content: `[{"id": "D", "kind": "data", "need": "x"}]`,
			want:    "PATH: record 1: id must match D<number>",
		},
		{
			name:    "id with wrong prefix",
			content: `[{"id": "d1", "kind": "data", "need": "x"}]`,
			want:    "PATH: record 1: id must match D<number>",
		},
		{
			name:    "id with trailing text",
			content: `[{"id": "D1a", "kind": "data", "need": "x"}]`,
			want:    "PATH: record 1: id must match D<number>",
		},
		{
			name:    "empty id",
			content: `[{"kind": "data", "need": "x"}]`,
			want:    "PATH: record 1: id must match D<number>",
		},
		{
			name: "unknown kind",
			content: `[
  {"id": "D1", "kind": "data", "need": "First."},
  {"id": "D2", "kind": "network", "need": "Second."}
]`,
			want: "PATH: record 2: kind must be data, state-write or flow",
		},
		{
			name:    "missing kind",
			content: `[{"id": "D1", "need": "x"}]`,
			want:    "PATH: record 1: kind must be data, state-write or flow",
		},
		{
			name:    "empty need",
			content: `[{"id": "D1", "kind": "data", "need": ""}]`,
			want:    "PATH: record 1: need must be one non-empty line",
		},
		{
			name:    "blank need",
			content: `[{"id": "D1", "kind": "data", "need": "  \t "}]`,
			want:    "PATH: record 1: need must be one non-empty line",
		},
		{
			name:    "multi-line need",
			content: `[{"id": "D1", "kind": "data", "need": "Line one.\nLine two."}]`,
			want:    "PATH: record 1: need must be one non-empty line",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := depsWriteFile(t, tc.content)

			got, err := LoadDependencies(path)
			if err == nil {
				t.Fatalf("LoadDependencies returned %#v and no error, want %q", got, tc.want)
			}
			if want := strings.ReplaceAll(tc.want, "PATH", path); err.Error() != want {
				t.Errorf("error text\n got: %s\nwant: %s", err, want)
			}
			if got != nil {
				t.Errorf("records = %#v, want nil on error", got)
			}
		})
	}

	t.Run("field of wrong type", func(t *testing.T) {
		path := depsWriteFile(t, `[{"id": 1, "kind": "data", "need": "x"}]`)

		_, err := LoadDependencies(path)
		if err == nil {
			t.Fatal("LoadDependencies returned no error for a field of the wrong type")
		}
		// The Go decoder words this error in its own way, so check the parts that matter.
		msg := err.Error()
		if !strings.HasPrefix(msg, path+": not valid JSON: ") || !strings.Contains(msg, "cannot unmarshal number") {
			t.Errorf("error %q does not report a JSON type error for path %s", msg, path)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.json")

		_, err := LoadDependencies(path)
		if err == nil {
			t.Fatal("LoadDependencies returned no error for a missing file")
		}
		if want := path + ": not found"; err.Error() != want {
			t.Errorf("error text\n got: %s\nwant: %s", err, want)
		}
	})

	t.Run("folder at the path", func(t *testing.T) {
		path := t.TempDir()

		_, err := LoadDependencies(path)
		if err == nil {
			t.Fatal("LoadDependencies returned no error for a folder")
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, path+": ") {
			t.Errorf("error %q does not start with the path", msg)
		}
		if strings.Contains(msg, "not found") {
			t.Errorf("error %q reports a folder as not found", msg)
		}
		if strings.Count(msg, path) != 1 {
			t.Errorf("error %q names the path more than once", msg)
		}
	})

	t.Run("file over the size limit", func(t *testing.T) {
		// The padding keeps the JSON valid, so only the size rule can fail.
		padding := strings.Repeat(" ", maxDependenciesBytes)
		path := depsWriteFile(t, "[]"+padding)

		_, err := LoadDependencies(path)
		if err == nil {
			t.Fatal("LoadDependencies returned no error for a file over the limit")
		}
		if want := path + ": larger than 256 KiB"; err.Error() != want {
			t.Errorf("error text\n got: %s\nwant: %s", err, want)
		}
	})

	t.Run("file at the size limit", func(t *testing.T) {
		padding := strings.Repeat(" ", maxDependenciesBytes-2)
		path := depsWriteFile(t, "[]"+padding)

		got, err := LoadDependencies(path)
		if err != nil {
			t.Fatalf("LoadDependencies: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %#v, want no records", got)
		}
	})
}
