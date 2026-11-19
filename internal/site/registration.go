package site

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nathanxiangang-web/drive/internal/store"
)

func (s *Site) register(w http.ResponseWriter, r *http.Request) {
	if !s.OpenRegistration {
		problem(w, 403, "当前未开放注册")
		return
	}
	if !s.registrationLimit.Allow() {
		problem(w, 429, "注册过于频繁，请稍后重试")
		return
	}
	var input struct{ Login, Password string }
	if !body(w, r, &input) {
		return
	}
	token, err := s.Store.Register(r.Context(), strings.TrimSpace(input.Login), input.Password)
	switch {
	case err == nil:
		sessionCookie(w, token)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.InvalidRegistrationLogin), errors.Is(err, store.InvalidRegistrationPassword), errors.Is(err, store.ReservedLogin):
		problem(w, 400, err.Error())
	case errors.Is(err, store.UserExists):
		problem(w, 409, err.Error())
	default:
		problem(w, 503, "暂时无法注册，请稍后重试")
	}
}
