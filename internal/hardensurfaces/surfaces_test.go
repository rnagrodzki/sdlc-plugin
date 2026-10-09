package hardensurfaces

import (
	"reflect"
	"testing"
)

// TestProposalIDs_ListOrder asserts that ProposalIDs returns the four proposal
// surface ids in the fixed order.
func TestProposalIDs_ListOrder(t *testing.T) {
	want := []string{"plan-guardrails", "execute-guardrails", "review-dimensions", "copilot-instructions"}
	if got := ProposalIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProposalIDs() = %v, want %v", got, want)
	}
}

// TestProposalIDs_AreListedSurfaces pins ProposalIDs to List: every proposal
// id is a listed surface, and the proposal ids are the leading entries of
// List in the same order.
func TestProposalIDs_AreListedSurfaces(t *testing.T) {
	list := List()
	ids := ProposalIDs()
	if len(ids) > len(list) {
		t.Fatalf("ProposalIDs() has %d ids, List() has %d surfaces", len(ids), len(list))
	}
	for i, id := range ids {
		if list[i].ID != id {
			t.Errorf("ProposalIDs()[%d] = %q, List()[%d].ID = %q", i, id, i, list[i].ID)
		}
	}
}

// TestProposalIDs_ReturnsNewSlice asserts that a caller who changes a slice
// returned by ProposalIDs does not change the result of the next call.
func TestProposalIDs_ReturnsNewSlice(t *testing.T) {
	first := ProposalIDs()
	first[0] = "changed"
	if got := ProposalIDs()[0]; got != "plan-guardrails" {
		t.Fatalf("ProposalIDs()[0] = %q after a caller changed an earlier result, want plan-guardrails", got)
	}
}
