// Package sets provides a set built on a map, in the shape of
// k8s.io/apimachinery/pkg/util/sets. That package predates generics and spells
// its sets out per element type; this one is generic over any comparable type,
// the way upstream's own Set[T] now is.
package sets

import (
	"cmp"
	"maps"
	"slices"
)

// Empty is the zero-width map value, so a set costs only its keys.
type Empty struct{}

// Set holds each element at most once. The zero value is not usable; build one
// with New or KeySet.
type Set[T comparable] map[T]Empty

// New builds a set holding items.
func New[T comparable](items ...T) Set[T] {
	s := make(Set[T], len(items))

	return s.Insert(items...)
}

// KeySet builds a set of the keys of source.
func KeySet[K comparable, V any](source map[K]V) Set[K] {
	s := make(Set[K], len(source))
	for key := range source {
		s[key] = Empty{}
	}

	return s
}

// Insert adds items to the set and returns it, so calls can be chained.
func (s Set[T]) Insert(items ...T) Set[T] {
	for _, item := range items {
		s[item] = Empty{}
	}

	return s
}

// InsertNew adds item and reports whether it was absent before, which is what
// de-duplicating a stream while keeping first-seen order needs.
func (s Set[T]) InsertNew(item T) bool {
	if _, ok := s[item]; ok {
		return false
	}

	s[item] = Empty{}

	return true
}

// Delete removes items from the set and returns it, so calls can be chained.
// Items that are not present are ignored.
func (s Set[T]) Delete(items ...T) Set[T] {
	for _, item := range items {
		delete(s, item)
	}

	return s
}

// Has reports whether item is in the set.
func (s Set[T]) Has(item T) bool {
	_, ok := s[item]

	return ok
}

// HasAll reports whether every one of items is in the set. It is true for no
// items at all.
func (s Set[T]) HasAll(items ...T) bool {
	for _, item := range items {
		if !s.Has(item) {
			return false
		}
	}

	return true
}

// HasAny reports whether at least one of items is in the set.
func (s Set[T]) HasAny(items ...T) bool {
	return slices.ContainsFunc(items, s.Has)
}

// Len reports how many elements the set holds.
func (s Set[T]) Len() int {
	return len(s)
}

// Clone returns a copy that can be changed without touching the original.
func (s Set[T]) Clone() Set[T] {
	return maps.Clone(s)
}

// Union returns the elements in either set.
func (s Set[T]) Union(other Set[T]) Set[T] {
	out := make(Set[T], len(s)+len(other))
	maps.Copy(out, s)
	maps.Copy(out, other)

	return out
}

// Intersection returns the elements in both sets.
func (s Set[T]) Intersection(other Set[T]) Set[T] {
	// Walking the smaller side keeps this proportional to it.
	small, large := s, other
	if len(other) < len(s) {
		small, large = other, s
	}

	out := make(Set[T], len(small))

	for item := range small {
		if large.Has(item) {
			out[item] = Empty{}
		}
	}

	return out
}

// Difference returns the elements in s that are not in other.
func (s Set[T]) Difference(other Set[T]) Set[T] {
	out := make(Set[T], len(s))

	for item := range s {
		if !other.Has(item) {
			out[item] = Empty{}
		}
	}

	return out
}

// IsSuperset reports whether every element of other is in s.
func (s Set[T]) IsSuperset(other Set[T]) bool {
	for item := range other {
		if !s.Has(item) {
			return false
		}
	}

	return true
}

// Equal reports whether both sets hold exactly the same elements.
func (s Set[T]) Equal(other Set[T]) bool {
	return len(s) == len(other) && s.IsSuperset(other)
}

// UnsortedList returns the elements in map order, which is deliberately
// unspecified. Use List when the order matters.
func (s Set[T]) UnsortedList() []T {
	return slices.Collect(maps.Keys(s))
}

// List returns the elements in sorted order. It is a function rather than a
// method because only orderable sets can be sorted.
func List[T cmp.Ordered](s Set[T]) []T {
	out := s.UnsortedList()
	slices.Sort(out)

	return out
}
