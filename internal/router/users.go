package router

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type RouterUserRole string

const (
	RouterUserRoleAdmin RouterUserRole = "admin"
	RouterUserRoleUser  RouterUserRole = "user"
)

type RouterUser struct {
	ID        string         `json:"id"`
	Email     string         `json:"email"`
	Name      string         `json:"name,omitempty"`
	Role      RouterUserRole `json:"role"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type RouterUserUpsertRequest struct {
	Email   string         `json:"email"`
	Name    string         `json:"name,omitempty"`
	Role    RouterUserRole `json:"role,omitempty"`
	Enabled *bool          `json:"enabled,omitempty"`
}

func (e *Engine) ListUsers() []RouterUser {
	if e == nil {
		return nil
	}
	e.usersMu.RLock()
	defer e.usersMu.RUnlock()
	out := make([]RouterUser, 0, len(e.users))
	for _, user := range e.users {
		out = append(out, user)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].Email < out[j].Email
	})
	return out
}

func (e *Engine) GetUserByEmail(email string) (RouterUser, bool) {
	if e == nil {
		return RouterUser{}, false
	}
	email = normalizeUserEmail(email)
	if email == "" {
		return RouterUser{}, false
	}
	e.usersMu.RLock()
	defer e.usersMu.RUnlock()
	user, ok := e.users[email]
	return user, ok
}

func (e *Engine) UpsertUser(request RouterUserUpsertRequest) (RouterUser, error) {
	if e == nil {
		return RouterUser{}, ErrRouterNotReady
	}
	email := normalizeUserEmail(request.Email)
	if email == "" {
		return RouterUser{}, fmt.Errorf("email is required")
	}
	role := normalizeRouterUserRole(request.Role)
	if role == "" {
		role = RouterUserRoleUser
	}
	now := time.Now().UTC()
	e.usersMu.Lock()
	defer e.usersMu.Unlock()
	if e.users == nil {
		e.users = make(map[string]RouterUser)
	}
	user := e.users[email]
	if user.ID == "" {
		user.ID = email
		user.Email = email
		user.CreatedAt = now
		user.Enabled = true
	}
	if request.Enabled != nil {
		user.Enabled = *request.Enabled
	}
	user.Name = strings.TrimSpace(request.Name)
	user.Role = role
	user.UpdatedAt = now
	e.users[email] = user
	return user, nil
}

func (e *Engine) DeleteUser(email string) bool {
	if e == nil {
		return false
	}
	email = normalizeUserEmail(email)
	if email == "" {
		return false
	}
	e.usersMu.Lock()
	defer e.usersMu.Unlock()
	if _, ok := e.users[email]; !ok {
		return false
	}
	delete(e.users, email)
	return true
}

func normalizeUserEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeRouterUserRole(role RouterUserRole) RouterUserRole {
	switch RouterUserRole(strings.ToLower(strings.TrimSpace(string(role)))) {
	case RouterUserRoleAdmin:
		return RouterUserRoleAdmin
	case RouterUserRoleUser, "":
		return RouterUserRoleUser
	default:
		return ""
	}
}

func (role RouterUserRole) IsAdmin() bool {
	return role == RouterUserRoleAdmin
}
