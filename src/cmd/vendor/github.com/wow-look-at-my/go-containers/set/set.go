// Package set provides a generic Set type backed by a Go map.
package set

import "fmt"

// Set is an unordered collection of unique elements of type T.
// The zero value is an empty set ready to use.
type Set[T comparable] struct {
	m map[T]struct{}
}

// New creates an empty set with optional initial capacity hint.
func New[T comparable](capacity ...int) Set[T] {
	var c int
	if len(capacity) > 0 {
		c = capacity[0]
	}
	return Set[T]{m: make(map[T]struct{}, c)}
}

// Of creates a set containing the given elements.
func Of[T comparable](elems ...T) Set[T] {
	s := Set[T]{m: make(map[T]struct{}, len(elems))}
	for _, e := range elems {
		s.m[e] = struct{}{}
	}
	return s
}

// Add inserts elem into the set. It returns true if the element was added,
// or false if it was already present.
func (s *Set[T]) Add(elem T) bool {
	if s.m == nil {
		s.m = make(map[T]struct{}, 1)
	}
	// One insert; a lookup-then-insert would hash the element twice.
	before := len(s.m)
	s.m[elem] = struct{}{}
	return len(s.m) != before
}

// AddRange inserts one or more elements into the set.
func (s *Set[T]) AddRange(elems ...T) {
	if s.m == nil {
		s.m = make(map[T]struct{}, len(elems))
	}
	for _, e := range elems {
		s.m[e] = struct{}{}
	}
}

// Remove deletes one or more elements from the set.
func (s *Set[T]) Remove(elems ...T) {
	if len(elems) == 1 {
		delete(s.m, elems[0])
		return
	}
	for _, e := range elems {
		delete(s.m, e)
	}
}

// Contains reports whether the set contains elem.
func (s Set[T]) Contains(elem T) bool {
	_, ok := s.m[elem]
	return ok
}

// ContainsAll reports whether the set contains every one of the given elements.
func (s Set[T]) ContainsAll(elems ...T) bool {
	for _, e := range elems {
		if _, ok := s.m[e]; !ok {
			return false
		}
	}
	return true
}

// ContainsAny reports whether the set contains at least one of the given elements.
func (s Set[T]) ContainsAny(elems ...T) bool {
	for _, e := range elems {
		if _, ok := s.m[e]; ok {
			return true
		}
	}
	return false
}

// Len returns the number of elements in the set.
func (s Set[T]) Len() int {
	return len(s.m)
}

// IsEmpty reports whether the set contains no elements.
func (s Set[T]) IsEmpty() bool {
	return len(s.m) == 0
}

// Clear removes all elements from the set.
func (s *Set[T]) Clear() {
	if s.m == nil {
		return
	}
	clear(s.m)
}

// Clone is a range loop, not maps.Clone: measured faster for empty-struct maps, and it's on the Union path.
func (s Set[T]) Clone() Set[T] {
	c := Set[T]{m: make(map[T]struct{}, len(s.m))}
	for k := range s.m {
		c.m[k] = struct{}{}
	}
	return c
}

// Values returns a slice containing all elements of the set in
// indeterminate order.
func (s Set[T]) Values() []T {
	v := make([]T, 0, len(s.m))
	for k := range s.m {
		v = append(v, k)
	}
	return v
}

// All returns an iterator over all elements of the set.
func (s Set[T]) All() func(yield func(T) bool) {
	return func(yield func(T) bool) {
		for k := range s.m {
			if !yield(k) {
				return
			}
		}
	}
}

// String returns a human-readable string representation of the set.
func (s Set[T]) String() string {
	return fmt.Sprintf("%v", s.Values())
}

// ---------- set-algebraic operations ----------

// Union returns a new set containing all elements that are in either s or other.
func (s Set[T]) Union(other Set[T]) Set[T] {
	// Start from the larger set to minimise iterations.
	big, small := s, other
	if big.Len() < small.Len() {
		big, small = small, big
	}
	out := Set[T]{m: make(map[T]struct{}, big.Len()+small.Len())}
	for k := range big.m {
		out.m[k] = struct{}{}
	}
	for k := range small.m {
		out.m[k] = struct{}{}
	}
	return out
}

// Intersection returns a new set containing only elements present in both s and other.
func (s Set[T]) Intersection(other Set[T]) Set[T] {
	// Iterate the smaller set for O(min(|s|, |other|)) lookups.
	small, big := s, other
	if small.Len() > big.Len() {
		small, big = big, small
	}
	out := New[T]()
	for k := range small.m {
		if _, ok := big.m[k]; ok {
			out.m[k] = struct{}{}
		}
	}
	return out
}

// Difference returns a new set containing elements in s that are not in other.
func (s Set[T]) Difference(other Set[T]) Set[T] {
	out := New[T]()
	for k := range s.m {
		if _, ok := other.m[k]; !ok {
			out.m[k] = struct{}{}
		}
	}
	return out
}

// SymmetricDifference returns a new set containing elements that are in
// exactly one of s or other.
func (s Set[T]) SymmetricDifference(other Set[T]) Set[T] {
	out := New[T]()
	for k := range s.m {
		if _, ok := other.m[k]; !ok {
			out.m[k] = struct{}{}
		}
	}
	for k := range other.m {
		if _, ok := s.m[k]; !ok {
			out.m[k] = struct{}{}
		}
	}
	return out
}

// IsSubsetOf reports whether every element of s is also in other.
func (s Set[T]) IsSubsetOf(other Set[T]) bool {
	if s.Len() > other.Len() {
		return false
	}
	for k := range s.m {
		if _, ok := other.m[k]; !ok {
			return false
		}
	}
	return true
}

// IsSupersetOf reports whether s contains every element of other.
func (s Set[T]) IsSupersetOf(other Set[T]) bool {
	return other.IsSubsetOf(s)
}

// IsProperSubsetOf reports whether s is a subset of other and s != other.
func (s Set[T]) IsProperSubsetOf(other Set[T]) bool {
	return s.Len() < other.Len() && s.IsSubsetOf(other)
}

// IsProperSupersetOf reports whether s is a superset of other and s != other.
func (s Set[T]) IsProperSupersetOf(other Set[T]) bool {
	return other.IsProperSubsetOf(s)
}

// Equal reports whether s and other contain exactly the same elements.
func (s Set[T]) Equal(other Set[T]) bool {
	if s.Len() != other.Len() {
		return false
	}
	for k := range s.m {
		if _, ok := other.m[k]; !ok {
			return false
		}
	}
	return true
}

// IsDisjoint reports whether s and other share no elements.
func (s Set[T]) IsDisjoint(other Set[T]) bool {
	small, big := s, other
	if small.Len() > big.Len() {
		small, big = big, small
	}
	for k := range small.m {
		if _, ok := big.m[k]; ok {
			return false
		}
	}
	return true
}

// ---------- in-place mutating operations ----------

// AddSet adds all elements from other into s.
func (s *Set[T]) AddSet(other Set[T]) {
	if len(other.m) == 0 {
		return
	}
	if s.m == nil {
		s.m = make(map[T]struct{}, len(other.m))
	}
	for k := range other.m {
		s.m[k] = struct{}{}
	}
}

// RemoveSet removes all elements of other from s.
func (s *Set[T]) RemoveSet(other Set[T]) {
	// When other is much larger, iterating s is cheaper.
	if s.Len() < other.Len() {
		for k := range s.m {
			if _, ok := other.m[k]; ok {
				delete(s.m, k)
			}
		}
	} else {
		for k := range other.m {
			delete(s.m, k)
		}
	}
}

// RetainAll removes every element from s that is not in other.
func (s *Set[T]) RetainAll(other Set[T]) {
	for k := range s.m {
		if _, ok := other.m[k]; !ok {
			delete(s.m, k)
		}
	}
}
