package users

import (
	"errors"
	"strings"

	"crypto/subtle"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (s *Store) UpdateProfile(id, fullName string) (User, error) {
	fullName = strings.TrimSpace(fullName)
	if len([]rune(fullName)) > 120 {
		return User{}, errors.New("full name is too long")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].ID == id {
			s.users[i].FullName = fullName
			if err := s.saveLocked(); err != nil {
				return User{}, err
			}
			return s.users[i], nil
		}
	}
	return User{}, errors.New("user not found")
}

func (s *Store) ChangePassword(id, current, next string) error {
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	s.mu.Lock()
	var index int
	var candidate User
	for i, u := range s.users {
		if u.ID == id {
			index, candidate = i, u
			break
		}
	}
	s.mu.Unlock()
	if candidate.ID == "" {
		return errors.New("user not found")
	}
	salt, err := cryptox.B64d(candidate.Salt)
	if err != nil {
		return ErrBadCredentials
	}
	want, err := cryptox.B64d(candidate.Verifier)
	if err != nil {
		return ErrBadCredentials
	}
	got := cryptox.Derive([]byte(current), salt)
	valid := subtle.ConstantTimeCompare(got, want) == 1
	cryptox.Zero(got)
	cryptox.Zero(salt)
	cryptox.Zero(want)
	if !valid {
		return ErrBadCredentials
	}
	newSalt, err := cryptox.Random(cryptox.SaltBytes)
	if err != nil {
		return err
	}
	key := cryptox.Derive([]byte(next), newSalt)
	defer cryptox.Zero(key)
	newSaltB64, keyB64 := cryptox.B64(newSalt), cryptox.B64(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= len(s.users) || s.users[index].ID != id {
		return errors.New("user changed while password was being updated")
	}
	oldSalt, oldVerifier := s.users[index].Salt, s.users[index].Verifier
	s.users[index].Salt, s.users[index].Verifier = newSaltB64, keyB64
	if err := s.saveLocked(); err != nil {
		s.users[index].Salt, s.users[index].Verifier = oldSalt, oldVerifier
		return err
	}
	s.invalidateSessionsLocked(id)
	return nil
}
