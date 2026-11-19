package site

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/status"
	"github.com/nathanxiangang-web/drive/internal/store"
)

func (s *Site) admin(handler func(http.ResponseWriter, *http.Request, store.Account)) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, a store.Account) {
		if a.Role != store.Admin {
			problem(w, http.StatusForbidden, "没有管理权限")
			return
		}
		handler(w, r, a)
	})
}

func (s *Site) adminStatus(w http.ResponseWriter, r *http.Request, _ store.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	database := s.Store.Ping(ctx) == nil
	openlist := s.Connection.Ping(ctx)
	respond(w, http.StatusOK, struct {
		Database   bool                 `json:"database"`
		OpenList   bool                 `json:"openlist"`
		Drive      cloud.LastRead       `json:"drive"`
		Server     status.Server        `json:"server"`
		Connection cloud.ConnectionInfo `json:"connection"`
	}{database, openlist, s.Drive.LastDirectoryRead(), status.Read(s.Started), s.Connection.Info()})
}

func (s *Site) adminUsers(w http.ResponseWriter, r *http.Request, _ store.Account) {
	users, err := s.Store.Users(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		problem(w, 503, "账号列表暂不可用")
		return
	}
	respond(w, 200, users)
}

func (s *Site) createUser(w http.ResponseWriter, r *http.Request, actor store.Account) {
	var input struct{ Login, Password, Role string }
	if !body(w, r, &input) {
		return
	}
	err := s.Store.CreateUser(r.Context(), actor.ID, strings.TrimSpace(input.Login), input.Password, input.Role)
	adminResult(w, err)
}

func (s *Site) changeUser(w http.ResponseWriter, r *http.Request, actor store.Account) {
	var input store.UserChange
	if !body(w, r, &input) {
		return
	}
	if input.Login != nil {
		login := strings.TrimSpace(*input.Login)
		input.Login = &login
	}
	adminResult(w, s.Store.ChangeUser(r.Context(), actor, r.PathValue("login"), input))
}

func (s *Site) deleteUser(w http.ResponseWriter, r *http.Request, actor store.Account) {
	adminResult(w, s.Store.DeleteUser(r.Context(), actor, r.PathValue("login")))
}

func (s *Site) configureOpenList(w http.ResponseWriter, r *http.Request, a store.Account) {
	if !s.configurationLimit.Allow() {
		problem(w, 429, "验证过于频繁，请稍后重试")
		return
	}
	var input cloud.OpenListCredentials
	if !body(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	adminResult(w, s.Connection.Configure(ctx, a.ID, input))
}

func adminResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.Forbidden):
		problem(w, 403, err.Error())
	case errors.Is(err, store.InvalidAccount), errors.Is(err, store.InvalidRegistrationPassword), errors.Is(err, cloud.InvalidConnection):
		problem(w, 400, err.Error())
	case errors.Is(err, store.ProtectedAdmin), errors.Is(err, store.UserExists):
		problem(w, 409, err.Error())
	case errors.Is(err, store.UserMissing):
		problem(w, 404, err.Error())
	case errors.Is(err, cloud.ConnectionKeyMissing):
		problem(w, 503, err.Error())
	default:
		problem(w, 503, "暂时无法保存，请稍后重试")
	}
}
