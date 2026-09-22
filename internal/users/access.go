package users

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (s *Store) RequestAccess(username, note string) (AccessRequest, error) {
	username = strings.TrimSpace(username)
	if !usernameRE.MatchString(username) {
		return AccessRequest{}, ErrBadUsername
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Username, username) {
			return AccessRequest{}, ErrUserExists
		}
	}
	for _, q := range s.requests {
		if q.Status == "pending" && strings.EqualFold(q.Username, username) {
			return AccessRequest{}, ErrUserExists
		}
	}
	b, err := cryptox.Random(16)
	if err != nil {
		return AccessRequest{}, err
	}
	q := AccessRequest{ID: hexToken(b), Username: username, Note: strings.TrimSpace(note), Status: "pending", CreatedAt: time.Now().UTC()}
	s.requests = append(s.requests, q)
	if err := s.saveLocked(); err != nil {
		s.requests = s.requests[:len(s.requests)-1]
		return AccessRequest{}, err
	}
	return q, nil
}

func (s *Store) ApproveAccess(id string) (AccessRequest, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.requests {
		q := &s.requests[i]
		if q.ID != id {
			continue
		}
		if q.Status != "pending" {
			return AccessRequest{}, "", errors.New("request is no longer pending")
		}
		for _, u := range s.users {
			if strings.EqualFold(u.Username, q.Username) {
				return AccessRequest{}, "", ErrUserExists
			}
		}
		b, err := cryptox.Random(32)
		if err != nil {
			return AccessRequest{}, "", err
		}
		token := hexToken(b)
		sum := sha256.Sum256([]byte(token))
		q.Status, q.ApprovedAt, q.TokenHash = "approved", time.Now().UTC(), hexToken(sum[:])
		if err := s.saveLocked(); err != nil {
			return AccessRequest{}, "", err
		}
		return *q, token, nil
	}
	return AccessRequest{}, "", errors.New("access request not found")
}

func (s *Store) RejectAccess(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.requests {
		if s.requests[i].ID == id {
			if s.requests[i].Status != "pending" {
				return errors.New("request is no longer pending")
			}
			s.requests[i].Status = "rejected"
			return s.saveLocked()
		}
	}
	return errors.New("access request not found")
}

func (s *Store) CompleteAccess(id, token, username, password string) (User, error) {
	if !usernameRE.MatchString(strings.TrimSpace(username)) || len(password) < 8 {
		return User{}, errors.New("invalid account details")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.requests {
		q := &s.requests[i]
		if q.ID != id || q.Username != strings.TrimSpace(username) {
			continue
		}
		if q.Status != "approved" || q.TokenHash == "" {
			return User{}, errors.New("approval is not available")
		}
		sum := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare([]byte(hexToken(sum[:])), []byte(q.TokenHash)) != 1 {
			return User{}, ErrBadCredentials
		}
		for _, u := range s.users {
			if strings.EqualFold(u.Username, username) {
				return User{}, ErrUserExists
			}
		}
		salt, err := cryptox.Random(cryptox.SaltBytes)
		if err != nil {
			return User{}, err
		}
		key := cryptox.Derive([]byte(password), salt)
		defer cryptox.Zero(key)
		idBytes, err := cryptox.Random(16)
		if err != nil {
			return User{}, err
		}
		u := User{ID: hexToken(idBytes), Username: strings.TrimSpace(username), Salt: cryptox.B64(salt), Verifier: cryptox.B64(key), CreatedAt: time.Now().UTC()}
		s.users = append(s.users, u)
		q.Status, q.ConsumedAt, q.TokenHash = "completed", time.Now().UTC(), ""
		if err := s.saveLocked(); err != nil {
			s.users = s.users[:len(s.users)-1]
			return User{}, err
		}
		if err := os.MkdirAll(filepath.Join(s.userRoot, u.ID), 0o700); err != nil {
			return User{}, err
		}
		return u, nil
	}
	return User{}, errors.New("access request not found")
}
