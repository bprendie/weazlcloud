package users

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
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
