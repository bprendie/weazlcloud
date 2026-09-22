package users

import "errors"

// SetDisabled persists the account state and revokes sessions. The last active
// administrator cannot be disabled.
func (s *Store) SetDisabled(id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 || s.users[idx].Deleting {
		return ErrDisabled
	}
	if disabled && s.users[idx].Admin && s.activeAdminsLocked() <= 1 {
		return errors.New("cannot disable the last active administrator")
	}
	old := s.users[idx]
	s.users[idx].Disabled = disabled
	if disabled {
		s.users[idx].DisablePending = true
		s.users[idx].DisableError = ""
	}
	if !disabled && (s.users[idx].DisableError != "" || s.users[idx].DisablePending) {
		s.users[idx] = old
		return errors.New("disable cleanup is incomplete")
	}
	if err := s.saveLocked(); err != nil {
		s.users[idx] = old
		return err
	}
	if disabled {
		s.invalidateSessionsLocked(id)
	}
	return nil
}

func (s *Store) SetDisableError(id, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return nil
	}
	old := s.users[idx]
	s.users[idx].DisableError = category
	if category == "" {
		s.users[idx].DisablePending = false
	}
	if err := s.saveLocked(); err != nil {
		s.users[idx] = old
		return err
	}
	return nil
}

func (s *Store) BeginDelete(id string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return User{}, errors.New("account not found")
	}
	u := s.users[idx]
	if !u.Deleting && u.Admin && !u.Disabled && s.activeAdminsLocked() <= 1 {
		return User{}, errors.New("cannot delete the last active administrator")
	}
	old := u
	u.Disabled, u.Deleting, u.DeleteError = true, true, ""
	s.users[idx] = u
	if err := s.saveLocked(); err != nil {
		s.users[idx] = old
		return User{}, err
	}
	s.invalidateSessionsLocked(id)
	return u, nil
}

func (s *Store) SetDeleteError(id, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return nil
	}
	old := s.users[idx]
	s.users[idx].DeleteError = category
	if err := s.saveLocked(); err != nil {
		s.users[idx] = old
		return err
	}
	return nil
}

func (s *Store) CompleteDelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return nil
	}
	old := s.users[idx]
	s.users = append(s.users[:idx], s.users[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		s.users = append(s.users, User{})
		copy(s.users[idx+1:], s.users[idx:])
		s.users[idx] = old
		return err
	}
	s.invalidateSessionsLocked(id)
	return nil
}

func (s *Store) indexLocked(id string) int {
	for i, u := range s.users {
		if u.ID == id {
			return i
		}
	}
	return -1
}
func (s *Store) activeAdminsLocked() int {
	n := 0
	for _, u := range s.users {
		if u.Admin && !u.Disabled && !u.Deleting {
			n++
		}
	}
	return n
}
