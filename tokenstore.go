package nalogo

import (
	"context"
	"encoding/json"
	"os"
	"sync"
)

// UserProfile holds the minimal user data returned alongside an access token.
type UserProfile struct {
	ID  string `json:"id"`
	INN string `json:"inn"`
}

// TokenData is the token payload persisted by TokenStore implementations.
// TokenExpireIn and RefreshTokenExpiresIn are strings in the FNS API response
// (ISO datetime or null); leave as json.RawMessage to tolerate both.
type TokenData struct {
	Token                 string          `json:"token"`
	RefreshToken          string          `json:"refreshToken"`
	TokenExpireIn         json.RawMessage `json:"tokenExpireIn,omitempty"`
	RefreshTokenExpiresIn json.RawMessage `json:"refreshTokenExpiresIn,omitempty"`
	Profile               UserProfile     `json:"profile"`
}

// TokenStore is the persistence port for token data.
// Implement to swap between storage backends.
type TokenStore interface {
	Save(ctx context.Context, t *TokenData) error
	Load(ctx context.Context) (*TokenData, error)
	Clear(ctx context.Context) error
}

// MemoryStore is a thread-safe in-memory TokenStore (default).
type MemoryStore struct {
	mu   sync.RWMutex
	data *TokenData
}

func (m *MemoryStore) Save(_ context.Context, t *TokenData) error {
	m.mu.Lock()
	m.data = t
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) Load(_ context.Context) (*TokenData, error) {
	m.mu.RLock()
	d := m.data
	m.mu.RUnlock()
	return d, nil
}

func (m *MemoryStore) Clear(_ context.Context) error {
	m.mu.Lock()
	m.data = nil
	m.mu.Unlock()
	return nil
}

// FileStore is a file-based TokenStore that persists token JSON to disk (mode 0600).
type FileStore struct {
	path string
}

// NewFileStore creates a FileStore that reads/writes to path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (f *FileStore) Save(_ context.Context, t *TokenData) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, b, 0600)
}

func (f *FileStore) Load(_ context.Context) (*TokenData, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		// Mirrors upstream: silently ignore file-not-found / parse errors
		return nil, nil //nolint:nilerr
	}
	var t TokenData
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, nil //nolint:nilerr
	}
	return &t, nil
}

func (f *FileStore) Clear(_ context.Context) error {
	err := os.Remove(f.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
