package set

import "encoding/json"

// MarshalJSON implements the json.Marshaler interface.
// The set is serialized as a JSON array of its elements.
func (s Set[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Values())
}

// UnmarshalJSON implements the json.Unmarshaler interface.
// The set is deserialized from a JSON array, replacing any existing elements.
func (s *Set[T]) UnmarshalJSON(data []byte) error {
	var elems []T
	if err := json.Unmarshal(data, &elems); err != nil {
		return err
	}
	if len(elems) == 0 {
		s.m = nil
		return nil
	}
	s.m = make(map[T]struct{}, len(elems))
	for _, e := range elems {
		s.m[e] = struct{}{}
	}
	return nil
}
