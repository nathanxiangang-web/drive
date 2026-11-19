package site

import (
	"net/http"
	"regexp"

	"github.com/nathanxiangang-web/drive/internal/store"
)

var taskID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (s *Site) clearTask(w http.ResponseWriter, r *http.Request, a store.Account) {
	id := r.PathValue("id")
	if !taskID.MatchString(id) {
		problem(w, 404, "任务不存在")
		return
	}
	found, err := s.Store.HideTask(r.Context(), a.ID, id)
	if err != nil {
		problem(w, 503, "暂时无法清除记录")
		return
	}
	if !found {
		problem(w, 404, "任务不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Site) clearTasks(w http.ResponseWriter, r *http.Request, a store.Account) {
	if err := s.Store.HideTasks(r.Context(), a.ID); err != nil {
		problem(w, 503, "暂时无法清空记录")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
