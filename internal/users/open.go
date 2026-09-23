package users

func New(path, userRoot string) (*Store, error) {
	return open(path, userRoot, true)
}

// NewReadOnly loads account metadata without writing compatibility upgrades.
func NewReadOnly(path, userRoot string) (*Store, error) {
	return open(path, userRoot, false)
}

func open(path, userRoot string, persistUpgrade bool) (*Store, error) {
	s := &Store{path: path, userRoot: userRoot, sessions: make(map[string]session)}
	if err := s.load(persistUpgrade); err != nil {
		return nil, err
	}
	return s, nil
}
