// Package mobileparts retains encrypted, independently addressed upload parts.
package mobileparts

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

const PartSize int64 = 16 << 20
const Lifetime = 24 * time.Hour

var (
	ErrInvalid    = errors.New("invalid mobile parts specification")
	ErrNotFound   = errors.New("mobile upload not found")
	ErrConflict   = errors.New("mobile upload identity conflict")
	ErrChecksum   = errors.New("mobile part checksum mismatch")
	ErrIncomplete = errors.New("mobile upload incomplete")
	ErrExpired    = errors.New("mobile upload staging expired")
	ErrCorrupt    = errors.New("mobile upload staging corrupt")
)

type Component struct {
	ID            string `json:"id"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	ReceivedParts int64  `json:"received_parts"`
	ReceivedBytes int64  `json:"received_bytes"`
}
type Spec struct {
	Kind               string          `json:"kind"`
	DeviceID           string          `json:"device_id"`
	Components         []Component     `json:"components"`
	CommitWhenComplete bool            `json:"commit_when_complete"`
	Payload            json.RawMessage `json:"payload"`
}
type Session struct {
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	OwnerID       string          `json:"owner_id"`
	Spec          Spec            `json:"spec"`
	Key           string          `json:"key"`
	Status        string          `json:"status"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	RetryAttempts int             `json:"retry_attempts,omitempty"`
	RetryAfter    time.Time       `json:"retry_after,omitempty"`
}
type View struct {
	ID         string          `json:"id"`
	Transport  string          `json:"transport"`
	Status     string          `json:"status"`
	Components []Component     `json:"components"`
	PartSize   int64           `json:"part_size"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
}
type PartPage struct {
	Missing []int64 `json:"missing"`
	Next    int64   `json:"next"`
	HasMore bool    `json:"has_more"`
}

func validID(s string) bool   { b, e := hex.DecodeString(s); return e == nil && len(b) == 16 }
func validHash(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func (s *Spec) normalize() error {
	if (s.Kind != "photo" && s.Kind != "file") || !validID(s.DeviceID) || len(s.Components) < 1 || len(s.Components) > 2 || len(s.Payload) > 65536 || !json.Valid(s.Payload) {
		return ErrInvalid
	}
	var total int64
	seen := map[string]bool{}
	for i := range s.Components {
		c := &s.Components[i]
		if (c.ID != "original" && c.ID != "motion") || seen[c.ID] || c.Size < 0 || c.Size > math.MaxInt64/4 || !validHash(c.SHA256) {
			return ErrInvalid
		}
		seen[c.ID] = true
		if total > math.MaxInt64/4-c.Size {
			return ErrInvalid
		}
		total += c.Size
		c.SHA256 = strings.ToLower(c.SHA256)
		c.ReceivedParts, c.ReceivedBytes = 0, 0
	}
	if !seen["original"] || s.Kind == "file" && len(s.Components) != 1 {
		return ErrInvalid
	}
	return nil
}
func count(c Component) int64 {
	if c.Size == 0 {
		return 0
	}
	return 1 + (c.Size-1)/PartSize
}
func partLength(c Component, index int64) (int64, error) {
	if index < 0 || index >= count(c) {
		return 0, ErrInvalid
	}
	return min(PartSize, c.Size-index*PartSize), nil
}
func ready(s Session) bool {
	for _, c := range s.Spec.Components {
		if c.ReceivedParts != count(c) || c.ReceivedBytes != c.Size {
			return false
		}
	}
	return true
}
func view(s Session) View {
	return View{s.ID, "parts-v1", s.Status, s.Spec.Components, PartSize, s.ErrorCode, s.Result, s.UpdatedAt, s.UpdatedAt.Add(Lifetime)}
}
