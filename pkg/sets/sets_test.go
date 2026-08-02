package sets_test

import (
	"slices"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

func TestNewDeduplicates(t *testing.T) {
	t.Parallel()

	s := sets.New("a", "b", "a")
	if s.Len() != 2 {
		t.Errorf("Len() = %d, want 2", s.Len())
	}

	if !s.HasAll("a", "b") {
		t.Errorf("want both elements, got %v", sets.List(s))
	}
}

func TestNewWithNoItems(t *testing.T) {
	t.Parallel()

	s := sets.New[string]()
	if s.Len() != 0 {
		t.Errorf("Len() = %d, want 0", s.Len())
	}

	// An empty set must still be usable.
	s.Insert("a")

	if !s.Has("a") {
		t.Error("Insert on an empty set did not take")
	}
}

func TestKeySet(t *testing.T) {
	t.Parallel()

	s := sets.KeySet(map[string]int{"b": 2, "a": 1})
	if got, want := sets.List(s), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestInsertNewReportsNovelty(t *testing.T) {
	t.Parallel()

	s := sets.New[string]()

	if !s.InsertNew("a") {
		t.Error("the first insert should report the element as new")
	}

	if s.InsertNew("a") {
		t.Error("the second insert should report the element as already present")
	}

	if s.Len() != 1 {
		t.Errorf("Len() = %d, want 1", s.Len())
	}
}

func TestInsertAndDeleteChain(t *testing.T) {
	t.Parallel()

	s := sets.New("a").Insert("b", "c").Delete("a", "absent")
	if got, want := sets.List(s), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestHasAllAndHasAny(t *testing.T) {
	t.Parallel()

	s := sets.New("a", "b")

	if !s.HasAll() {
		t.Error("HasAll() with no items should be true")
	}

	if s.HasAny() {
		t.Error("HasAny() with no items should be false")
	}

	if !s.HasAll("a", "b") || s.HasAll("a", "z") {
		t.Error("HasAll is wrong")
	}

	if !s.HasAny("z", "b") || s.HasAny("y", "z") {
		t.Error("HasAny is wrong")
	}
}

func TestUnionIntersectionDifference(t *testing.T) {
	t.Parallel()

	a := sets.New("a", "b", "c")
	b := sets.New("c", "d")

	tests := map[string]struct {
		got  []string
		want []string
	}{
		"union":                 {got: sets.List(a.Union(b)), want: []string{"a", "b", "c", "d"}},
		"union is symmetric":    {got: sets.List(b.Union(a)), want: []string{"a", "b", "c", "d"}},
		"intersection":          {got: sets.List(a.Intersection(b)), want: []string{"c"}},
		"intersection reversed": {got: sets.List(b.Intersection(a)), want: []string{"c"}},
		"difference":            {got: sets.List(a.Difference(b)), want: []string{"a", "b"}},
		"difference reversed":   {got: sets.List(b.Difference(a)), want: []string{"d"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestOperationsLeaveTheOperandsAlone(t *testing.T) {
	t.Parallel()

	a := sets.New("a", "b")
	b := sets.New("b", "c")

	a.Union(b)
	a.Intersection(b)
	a.Difference(b)

	if got, want := sets.List(a), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("a changed: got %v, want %v", got, want)
	}

	if got, want := sets.List(b), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("b changed: got %v, want %v", got, want)
	}
}

func TestEqualAndIsSuperset(t *testing.T) {
	t.Parallel()

	a := sets.New("a", "b")

	if !a.Equal(sets.New("b", "a")) {
		t.Error("order must not matter for Equal")
	}

	if a.Equal(sets.New("a")) || a.Equal(sets.New("a", "b", "c")) {
		t.Error("sets of different sizes must not be equal")
	}

	if !a.IsSuperset(sets.New("a")) || !a.IsSuperset(sets.New[string]()) {
		t.Error("IsSuperset should hold for subsets and the empty set")
	}

	if a.IsSuperset(sets.New("a", "z")) {
		t.Error("IsSuperset should not hold for a set with an extra element")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	t.Parallel()

	original := sets.New("a")

	clone := original.Clone()
	clone.Insert("b")

	if original.Has("b") {
		t.Error("changing the clone must not touch the original")
	}
}

func TestUnsortedListHoldsEveryElement(t *testing.T) {
	t.Parallel()

	s := sets.New(3, 1, 2)

	got := s.UnsortedList()
	slices.Sort(got)

	if want := []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("UnsortedList() = %v, want %v once sorted", got, want)
	}
}
