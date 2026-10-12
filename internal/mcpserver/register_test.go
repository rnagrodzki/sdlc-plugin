package mcpserver

import (
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// --- registry test input types ---

// actionIn mirrors the tag style of the production action tools, for example
// ship_state: an "action" field with a jsonschema enum.
type actionIn struct {
	Action string `json:"action" jsonschema:"enum=init,enum=read,enum=wave-done" jsonschema_description:"Selects the operation."`
	Extra  string `json:"extra,omitempty"`
}

// otherActionIn has a different enum, to prove the last registration wins.
type otherActionIn struct {
	Action string `json:"action" jsonschema:"enum=start,enum=stop"`
}

// freeActionIn has an "action" field with no enum.
type freeActionIn struct {
	Action string `json:"action"`
}

// registryOut is the shared output type of the registry fixtures.
type registryOut struct {
	OK bool `json:"ok"`
}

// registryAnnotations returns the annotations that every registry fixture uses.
func registryAnnotations() Annotations {
	return Annotations{Title: "Registry fixture", ReadOnly: true, Idempotent: true}
}

// registerFixture registers a tool named name, with TIn as its input type, on srv.
func registerFixture[TIn any](srv *Server, name string) {
	Register(srv, name, "fixture", registryAnnotations(), func(_ Ctx, _ TIn) (registryOut, error) {
		return registryOut{OK: true}, nil
	})
}

// TestToolNames_EmptyServerReturnsNonNil verifies that a server with no tool
// gives an empty, non-nil name list.
func TestToolNames_EmptyServerReturnsNonNil(t *testing.T) {
	names := New("test", "0.0.0-test").ToolNames()
	if names == nil {
		t.Fatal("ToolNames() = nil, want a non-nil empty slice")
	}
	if len(names) != 0 {
		t.Fatalf("ToolNames() = %v, want empty", names)
	}
}

// TestToolNames_SortedRegardlessOfRegistrationOrder verifies that the names
// come back sorted, whatever the order of registration.
func TestToolNames_SortedRegardlessOfRegistrationOrder(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[echoIn](srv, "zz_echo")
	registerFixture[actionIn](srv, "ship_state")
	registerFixture[echoIn](srv, "aa_first")

	want := []string{"aa_first", "ship_state", "zz_echo"}
	if got := srv.ToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolNames() = %v, want %v", got, want)
	}
}

// TestToolActions_ReturnsEnumInSchemaOrder verifies that the actions of a tool
// are the values of its "action" enum, in schema order.
func TestToolActions_ReturnsEnumInSchemaOrder(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[actionIn](srv, "ship_state")

	got, ok := srv.ToolActions("ship_state")
	if !ok {
		t.Fatal("ToolActions(ship_state) ok = false, want true")
	}
	want := []string{"init", "read", "wave-done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolActions(ship_state) = %v, want %v", got, want)
	}
}

// TestToolActions_ToolWithoutActionFieldIsEmptyNonNil verifies that a tool
// with no "action" field gives an empty, non-nil list and ok true.
func TestToolActions_ToolWithoutActionFieldIsEmptyNonNil(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[echoIn](srv, "zz_echo")

	got, ok := srv.ToolActions("zz_echo")
	if !ok {
		t.Fatal("ToolActions(zz_echo) ok = false, want true for a registered tool")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ToolActions(zz_echo) = %#v, want a non-nil empty slice", got)
	}
}

// TestToolActions_ActionFieldWithoutEnumIsEmptyNonNil verifies that an
// "action" field with no enum gives an empty, non-nil list and ok true.
func TestToolActions_ActionFieldWithoutEnumIsEmptyNonNil(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[freeActionIn](srv, "free_action")

	got, ok := srv.ToolActions("free_action")
	if !ok {
		t.Fatal("ToolActions(free_action) ok = false, want true for a registered tool")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ToolActions(free_action) = %#v, want a non-nil empty slice", got)
	}
}

// TestToolActions_UnknownNameIsEmptyNonNilAndFalse verifies that an unknown
// tool name gives an empty, non-nil list and ok false.
func TestToolActions_UnknownNameIsEmptyNonNilAndFalse(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[actionIn](srv, "ship_state")

	got, ok := srv.ToolActions("no_such_tool")
	if ok {
		t.Fatal("ToolActions(no_such_tool) ok = true, want false")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ToolActions(no_such_tool) = %#v, want a non-nil empty slice", got)
	}
}

// TestRegister_SecondRegistrationKeepsOneEntryWithLastSchema verifies that a
// second registration of one name keeps one entry, with the last schema.
func TestRegister_SecondRegistrationKeepsOneEntryWithLastSchema(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[actionIn](srv, "dup_tool")
	registerFixture[otherActionIn](srv, "dup_tool")

	if got, want := srv.ToolNames(), []string{"dup_tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolNames() = %v, want %v", got, want)
	}
	got, ok := srv.ToolActions("dup_tool")
	if !ok {
		t.Fatal("ToolActions(dup_tool) ok = false, want true")
	}
	if want := []string{"start", "stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolActions(dup_tool) = %v, want the last schema's %v", got, want)
	}
}

// TestToolActions_ReturnedSliceIsACopy verifies that a change to the returned
// slice does not change what the server stores.
func TestToolActions_ReturnedSliceIsACopy(t *testing.T) {
	srv := New("test", "0.0.0-test")
	registerFixture[actionIn](srv, "ship_state")

	first, _ := srv.ToolActions("ship_state")
	first[0] = "mutated"

	second, _ := srv.ToolActions("ship_state")
	if second[0] != "init" {
		t.Fatalf("ToolActions(ship_state)[0] = %q after a caller change, want %q", second[0], "init")
	}
}

// TestActionEnum_NilSchemaIsEmptyNonNil verifies that a nil input schema gives
// an empty, non-nil slice and does not panic.
func TestActionEnum_NilSchemaIsEmptyNonNil(t *testing.T) {
	got := actionEnum(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("actionEnum(nil) = %#v, want a non-nil empty slice", got)
	}
}

// TestActionEnum_HandBuiltSchemas verifies actionEnum on schemas that the
// schema generator does not produce: no properties, a nil property, a property
// with no enum, and an enum with members that are not strings. Only the string
// members are returned, in order.
func TestActionEnum_HandBuiltSchemas(t *testing.T) {
	tests := []struct {
		name   string
		schema *jsonschema.Schema
		want   []string
	}{
		{"no properties", &jsonschema.Schema{}, []string{}},
		{"no action property", &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"other": {}}}, []string{}},
		{"nil action property", &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"action": nil}}, []string{}},
		{"action property without enum", &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"action": {Type: "string"}}}, []string{}},
		{
			"non-string enum members are skipped",
			&jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"action": {Enum: []any{"init", 7, nil, true, "read"}}}},
			[]string{"init", "read"},
		},
		{
			"enum with only non-string members",
			&jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"action": {Enum: []any{1, 2.5}}}},
			[]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionEnum(tt.schema)
			if got == nil {
				t.Fatal("actionEnum returned nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("actionEnum = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestRecordTool_NilActionsStoredAsEmptyNonNil verifies that recordTool with a
// nil actions slice stores an empty slice, so ToolActions never returns nil
// for a registered tool.
func TestRecordTool_NilActionsStoredAsEmptyNonNil(t *testing.T) {
	srv := New("test", "0.0.0-test")
	srv.recordTool("no_actions", nil)

	got, ok := srv.ToolActions("no_actions")
	if !ok {
		t.Fatal("ToolActions(no_actions) ok = false, want true for a recorded tool")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ToolActions(no_actions) = %#v, want a non-nil empty slice", got)
	}
}

// TestServerZeroValue_NilToolsMap verifies the nil-map guards of a Server that
// New did not build: ToolNames and ToolActions read an absent map without a
// panic and return non-nil empty results, and recordTool makes the map before
// it stores the first tool.
func TestServerZeroValue_NilToolsMap(t *testing.T) {
	srv := &Server{}

	if names := srv.ToolNames(); names == nil || len(names) != 0 {
		t.Fatalf("ToolNames() = %#v, want a non-nil empty slice", names)
	}
	if actions, ok := srv.ToolActions("anything"); ok || actions == nil || len(actions) != 0 {
		t.Fatalf("ToolActions(anything) = %#v, %v, want a non-nil empty slice and false", actions, ok)
	}

	srv.recordTool("late", []string{"a", "b"})
	if got, want := srv.ToolNames(), []string{"late"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolNames() = %v, want %v", got, want)
	}
	if got, ok := srv.ToolActions("late"); !ok || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("ToolActions(late) = %v, %v, want [a b] and true", got, ok)
	}
}
