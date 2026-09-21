package users

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

var (
	ErrNoUsers        = errors.New("no users are configured")
	ErrUserExists     = errors.New("user already exists")
	ErrBadCredentials = errors.New("incorrect username or password")
	ErrDisabled       = errors.New("user is disabled")
	ErrNoSession      = errors.New("authentication required")
	ErrBadUsername    = errors.New("username must be 3-32 letters, numbers, dots, dashes, or underscores")
)

const cookieName = "weazl_session"
const sessionTTL = 24 * time.Hour

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,31}$`)

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	FullName  string    `json:"full_name,omitempty"`
	Admin     bool      `json:"admin"`
	Disabled  bool      `json:"disabled"`
	Salt      string    `json:"salt"`
	Verifier  string    `json:"verifier"`
	CreatedAt time.Time `json:"created_at"`
}

type session struct {
	userID  string
	expires time.Time
}

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

type AccessRequest struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Note       string    `json:"note,omitempty"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ApprovedAt time.Time `json:"approved_at,omitempty"`
	TokenHash  string    `json:"token_hash,omitempty"`
	ConsumedAt time.Time `json:"consumed_at,omitempty"`
}

type file struct {
	Users          []User          `json:"users"`
	AccessRequests []AccessRequest `json:"access_requests,omitempty"`
}

type Store struct {
	mu            sync.Mutex
	path          string
	userRoot      string
	users         []User
	requests      []AccessRequest
	sessions      map[string]session
	secureCookies bool
}

func New(path, userRoot string) (*Store, error) {
	s := &Store{path: path, userRoot: userRoot, sessions: make(map[string]session)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	s.users = f.Users
	s.requests = f.AccessRequests
	return nil
}

func (s *Store) AccessRequests() []AccessRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AccessRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

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

func (s *Store) Count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.users) }

func (s *Store) Users() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, len(s.users))
	copy(out, s.users)
	return out
}

func (s *Store) User(id string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

func (s *Store) Create(username, password string, admin bool) (User, error) {
	username = strings.TrimSpace(username)
	if !usernameRE.MatchString(username) {
		return User{}, ErrBadUsername
	}
	if len(password) < 8 {
		return User{}, errors.New("password must be at least 8 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	u := User{ID: hexToken(idBytes), Username: username, Admin: admin, Salt: cryptox.B64(salt), Verifier: cryptox.B64(key), CreatedAt: time.Now().UTC()}
	s.users = append(s.users, u)
	if err := s.saveLocked(); err != nil {
		s.users = s.users[:len(s.users)-1]
		return User{}, err
	}
	if err := os.MkdirAll(s.userRoot+"/"+u.ID, 0o700); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) Authenticate(username, password string) (User, error) {
	s.mu.Lock()
	var candidate User
	for _, u := range s.users {
		if !strings.EqualFold(u.Username, strings.TrimSpace(username)) {
			continue
		}
		if u.Disabled {
			s.mu.Unlock()
			return User{}, ErrDisabled
		}
		candidate = u
		break
	}
	s.mu.Unlock()
	if candidate.ID == "" {
		return User{}, ErrBadCredentials
	}
	salt, err := cryptox.B64d(candidate.Salt)
	if err != nil {
		return User{}, ErrBadCredentials
	}
	want, err := cryptox.B64d(candidate.Verifier)
	if err != nil {
		return User{}, ErrBadCredentials
	}
	got := cryptox.Derive([]byte(password), salt)
	defer cryptox.Zero(got)
	if subtle.ConstantTimeCompare(got, want) == 1 {
		return candidate, nil
	}
	return User{}, ErrBadCredentials
}

func (s *Store) Login(u User) (string, error) {
	b, err := cryptox.Random(32)
	if err != nil {
		return "", err
	}
	token := hexToken(b)
	s.mu.Lock()
	s.sessions[token] = session{userID: u.ID, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	return token, nil
}

func (s *Store) Current(r *http.Request) (User, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return User{}, ErrNoSession
	}
	s.mu.Lock()
	sess, ok := s.sessions[c.Value]
	s.mu.Unlock()
	if !ok || time.Now().After(sess.expires) {
		if ok {
			s.mu.Lock()
			delete(s.sessions, c.Value)
			s.mu.Unlock()
		}
		return User{}, ErrNoSession
	}
	u, ok := s.User(sess.userID)
	if !ok || u.Disabled {
		return User{}, ErrNoSession
	}
	return u, nil
}

func (s *Store) SetSession(w http.ResponseWriter, token string) {
	s.mu.Lock()
	secure := s.secureCookies
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
}

func ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: false, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (s *Store) SetSecureCookies(secure bool) {
	s.mu.Lock()
	s.secureCookies = secure
	s.mu.Unlock()
}

func (s *Store) Logout(r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
}

func (s *Store) invalidateSessionsLocked(userID string) {
	for token, sess := range s.sessions {
		if sess.userID == userID {
			delete(s.sessions, token)
		}
	}
}

func (s *Store) VaultPath(u User) string   { return filepath.Join(s.userRoot, u.ID, "vault.json") }
func (s *Store) NodeKeyPath(u User) string { return filepath.Join(s.userRoot, u.ID, "node.key") }
func (s *Store) LibraryPath(u User) string { return filepath.Join(s.userRoot, u.ID, "library") }
func (s *Store) CatalogPath(u User) string { return filepath.Join(s.userRoot, u.ID, "catalog.enc") }
func (s *Store) PlacesPath(u User) string  { return filepath.Join(s.userRoot, u.ID, "places.json") }
func (s *Store) LegacyVaultPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "vault.json")
}
func (s *Store) LegacyNodeKeyPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "node.key")
}
func (s *Store) LegacyLibraryPath() string { return filepath.Join(filepath.Dir(s.userRoot), "library") }
func (s *Store) LegacyCatalogPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "catalog.enc")
}
func (s *Store) LegacyPlacesPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "places.json")
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(file{Users: s.users, AccessRequests: s.requests}, "", "  ")
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(s.path, append(b, '\n'), 0o600)
}

func hexToken(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = hex[v>>4], hex[v&0xf]
	}
	return string(out)
}
