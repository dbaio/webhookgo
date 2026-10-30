package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/auth"
)

const (
	errBadKey      = "Invalid API key"
	errKeyNotFound = "API key not found"
)

// authenticateKey resolves an API key (Api-Key header or Bearer token) into
// its user, on the /token routes only, when no session cookie did: a valid
// session wins. A key that is present must be valid; there is no silent fall
// back to anonymous. On /api/* the headers are ignored, so a key cannot
// manage accounts, keys or admin.
func (s *Server) authenticateKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ""
		if sessionFrom(r) == nil && strings.HasPrefix(r.URL.Path, "/token") {
			key = auth.APIKey(r)
		}
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.keyUser(r.Context(), key)
		if errors.Is(err, accounts.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, errBadKey)
			return
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		// No secret: a key is not a session and cannot end one.
		ctx := context.WithValue(r.Context(), sessionKey, &session{user: u})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// keyUser resolves a key to its user, who acts with the plain user role
// whatever the account's own: a key never grants admin access to other
// people's private URLs.
func (s *Server) keyUser(ctx context.Context, key string) (*accounts.User, error) {
	u, err := s.accounts.APIKeyUser(ctx, key)
	if err != nil {
		return nil, err
	}
	u.Role = accounts.RoleUser
	return u, nil
}

// --- Session-only routes ------------------------------------------------------

// newKeyView is the answer to a created key: the only time the key appears.
type newKeyView struct {
	*accounts.APIKey
	Key string `json:"key"`
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	keys, err := s.accounts.ListAPIKeys(r.Context(), sess.user.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": keys})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := validName(name); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	k, key, err := s.accounts.CreateAPIKey(r.Context(), sess.user.ID, name)
	if errors.Is(err, accounts.ErrTooManyKeys) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("You can have at most %d API keys", accounts.MaxAPIKeys))
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, newKeyView{APIKey: k, Key: key})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	id := strings.ToLower(r.PathValue("key"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, errKeyNotFound)
		return
	}
	err := s.accounts.RevokeAPIKey(r.Context(), sess.user.ID, id)
	if errors.Is(err, accounts.ErrNotFound) {
		writeError(w, http.StatusNotFound, errKeyNotFound)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
