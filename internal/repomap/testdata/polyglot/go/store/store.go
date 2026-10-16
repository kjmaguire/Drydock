package store

// Store keeps values by key.
type Store struct {
	values map[string]string
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{values: map[string]string{}}
}

// LookupValue returns the value of key.
func (s *Store) LookupValue(key string) string {
	return s.values[key]
}

func (s *Store) SaveValue(key, value string) {
	s.values[key] = value
}
