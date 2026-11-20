package site

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/nathanxiangang-web/drive/internal/store"
)

func (s *Site) adminShares(w http.ResponseWriter, r *http.Request, a store.Account) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	start, err := offset(r.URL.Query().Get("offset"))
	if err != nil || utf8.RuneCountInString(query) > 120 {
		problem(w, 400, "查询参数无效")
		return
	}
	items, more, err := s.Store.AdminShares(r.Context(), a.ID, query, start)
	if err != nil {
		problem(w, 503, "分享记录暂不可用")
		return
	}
	respond(w, 200, struct {
		Items []store.AdminShare `json:"items"`
		More  bool               `json:"more"`
	}{items, more})
}

func (s *Site) adminShareCode(w http.ResponseWriter, r *http.Request, a store.Account) {
	token := r.PathValue("token")
	if !shareToken.MatchString(token) {
		problem(w, 404, store.ShareMissing.Error())
		return
	}
	var input struct{ Code string }
	if !bodyWithLimit(w, r, &input, 1024) {
		return
	}
	item, err := s.Store.AdminShareCode(r.Context(), a.ID, token, input.Code)
	if errors.Is(err, store.InvalidShareCode) {
		problem(w, 400, err.Error())
		return
	}
	if !shareMutationError(w, err) {
		respond(w, 200, item)
	}
}

func (s *Site) adminRevokeShare(w http.ResponseWriter, r *http.Request, a store.Account) {
	token := r.PathValue("token")
	if !shareToken.MatchString(token) {
		problem(w, 404, store.ShareMissing.Error())
		return
	}
	if !shareMutationError(w, s.Store.AdminRevokeShare(r.Context(), a.ID, token)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func shareMutationError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, store.ShareMissing) {
		problem(w, 404, err.Error())
	} else if err != nil {
		problem(w, 503, "暂时无法更新分享")
	}
	return err != nil
}
