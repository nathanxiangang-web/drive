package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

var shareToken = regexp.MustCompile(`^(?:[A-Za-z0-9_-]{16}|[0-9a-f]{64})$`)
var providerFileID = regexp.MustCompile(`^[0-9]{1,32}$`)

func (s *Site) createShare(w http.ResponseWriter, r *http.Request, a store.Account) {
	var input struct {
		Area, Dir, Offset, ID, Ref string
		Days                       int
		Code                       string
	}
	if !body(w, r, &input) {
		return
	}
	if input.Days != 1 && input.Days != 7 && input.Days != 30 {
		problem(w, 400, "请选择 1、7 或 30 天有效期")
		return
	}
	if input.Code == "" {
		input.Code = store.RandomShareCode()
	}
	code, ok := store.ShareCode(input.Code)
	if !ok {
		problem(w, 400, store.InvalidShareCode.Error())
		return
	}
	q := url.Values{"area": {input.Area}, "dir": {input.Dir}, "offset": {input.Offset}, "id": {input.ID}, "ref": {input.Ref}}
	ctx, cancel := context.WithTimeout(r.Context(), 22*time.Second)
	defer cancel()
	selected, err := s.selectFile(ctx, a, q)
	if err == nil {
		err = s.Drive.CheckFile(ctx, selected.Item, selected.Parent)
	}
	if err != nil {
		fileFailure(w, err)
		return
	}
	result, err := s.Store.CreateShare(ctx, store.Share{Owner: a.ID, Area: selected.Area, Dir: selected.Dir,
		Parent: selected.Parent, FileID: selected.Item.ID, Name: selected.Item.Name, Size: selected.Item.Size, Code: code}, input.Days)
	if err != nil {
		problem(w, 503, "暂时无法创建分享")
		return
	}
	respond(w, 201, result)
}

func (s *Site) newShareCode(w http.ResponseWriter, _ *http.Request, _ store.Account) {
	respond(w, 200, struct {
		Code string `json:"code"`
	}{store.RandomShareCode()})
}

func (s *Site) listShares(w http.ResponseWriter, r *http.Request, a store.Account) {
	file := r.URL.Query().Get("file")
	if !providerFileID.MatchString(file) {
		problem(w, 400, "请选择文件")
		return
	}
	shares, err := s.Store.Shares(r.Context(), a.ID, file, 100, 0)
	if err != nil {
		problem(w, 503, "分享记录暂不可用")
		return
	}
	respond(w, 200, shares)
}

func (s *Site) myShares(w http.ResponseWriter, r *http.Request, a store.Account) {
	const pageSize = 50
	start, err := offset(r.URL.Query().Get("offset"))
	if err != nil {
		problem(w, 400, "页码无效")
		return
	}
	shares, err := s.Store.Shares(r.Context(), a.ID, "", pageSize+1, start)
	if err != nil {
		problem(w, 503, "分享记录暂不可用")
		return
	}
	more := len(shares) > pageSize
	if more {
		shares = shares[:pageSize]
	}
	respond(w, 200, struct {
		Items []store.Share `json:"items"`
		More  bool          `json:"more"`
	}{shares, more})
}

func (s *Site) revokeShare(w http.ResponseWriter, r *http.Request, a store.Account) {
	token := r.PathValue("token")
	if !shareToken.MatchString(token) {
		problem(w, 404, store.ShareMissing.Error())
		return
	}
	if !shareMutationError(w, s.Store.RevokeShare(r.Context(), a.ID, token)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

type shareView struct {
	Name, Format, Size, Expires, ExpiresISO string
	Style, Script, Background, Download     string
	Message, Hint, Back                     string
	Unlock                                  string
	Available, Playable                     bool
}

type sharedPages struct {
	site                      *Site
	page                      *template.Template
	style, script, background string
}

func (s *Site) publicShareRoutes(mux *http.ServeMux, static map[string]string) {
	p := sharedPages{site: s, page: template.Must(template.ParseFS(assets, "web/share.html")),
		style: static["share.css"], script: static["share-view.js"], background: static["share-bg.webp"]}
	mux.HandleFunc("GET /s/{token}", p.open)
	mux.HandleFunc("GET /s/{token}/download", p.download)
	mux.HandleFunc("POST /s/{token}/unlock", p.unlock)
}

func (p sharedPages) render(w http.ResponseWriter, code int, view shareView) {
	view.Style, view.Script, view.Background = p.style, p.script, p.background
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	var result bytes.Buffer
	if p.page.ExecuteTemplate(&result, "share.html", view) != nil {
		http.Error(w, "分享页面暂不可用", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(result.Bytes())
}

func (p sharedPages) invalid(w http.ResponseWriter) {
	p.render(w, 410, shareView{Message: "此分享链接已失效", Hint: "请联系分享者获取新链接。"})
}

func (p sharedPages) unavailable(w http.ResponseWriter, token string) {
	back := ""
	if shareToken.MatchString(token) {
		back = "/s/" + token
	}
	p.render(w, 503, shareView{Message: "文件暂时无法打开", Hint: "网盘没有返回可用链接，请稍后重试。", Back: back})
}

func (p sharedPages) usable(w http.ResponseWriter, r *http.Request, lockedStatus int) (store.Share, bool) {
	token := r.PathValue("token")
	if !shareToken.MatchString(token) {
		p.invalid(w)
		return store.Share{}, false
	}
	share, err := p.site.Store.PublicShare(r.Context(), token)
	if errors.Is(err, store.ShareMissing) {
		p.invalid(w)
		return store.Share{}, false
	}
	if err != nil {
		p.unavailable(w, token)
		return store.Share{}, false
	}
	if !p.hasAccess(r, share) {
		p.locked(w, token, lockedStatus, "")
		return store.Share{}, false
	}
	return share, true
}

func (p sharedPages) open(w http.ResponseWriter, r *http.Request) {
	share, ok := p.usable(w, r, http.StatusOK)
	if !ok {
		return
	}
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		zone = time.FixedZone("CST", 8*60*60)
	}
	ext := strings.ToUpper(strings.TrimPrefix(path.Ext(share.Name), "."))
	playable := false
	switch ext {
	case "MP4", "WEBM", "MOV", "M4V", "MKV", "AVI", "MP3", "M4A", "OGG", "WAV", "FLAC":
		playable = true
	}
	if ext == "" || len(ext) > 10 {
		ext = "文件"
	}
	p.render(w, 200, shareView{Name: share.Name, Format: ext, Size: shareSize(share.Size), Available: true, Playable: playable,
		Expires: share.Expires.In(zone).Format("2006-01-02 15:04"), ExpiresISO: share.Expires.Format(time.RFC3339),
		Download: "/s/" + share.Token + "/download"})
}

func (p sharedPages) download(w http.ResponseWriter, r *http.Request) {
	share, ok := p.usable(w, r, http.StatusForbidden)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 22*time.Second)
	defer cancel()
	a, err := p.site.Store.User(ctx, share.Owner)
	if errors.Is(err, pgx.ErrNoRows) {
		p.invalid(w)
		return
	}
	if err != nil {
		p.unavailable(w, share.Token)
		return
	}
	base, err := p.site.directory(ctx, a, share.Area, share.Dir)
	item := cloud.Item{ID: share.FileID, Name: share.Name, Size: share.Size}
	if err == nil && !p.site.searchable(share.Area, share.Dir, item) {
		err = cloud.Missing
	}
	if err == nil {
		var page cloud.Page
		page, err = p.site.Drive.List(ctx, base, 0, false)
		if err == nil && page.CID != share.Parent {
			err = cloud.Missing
		}
	}
	if err == nil {
		err = p.site.Drive.CheckFile(ctx, item, share.Parent)
	}
	if errors.Is(err, cloud.Missing) {
		p.invalid(w)
		return
	}
	if err != nil {
		p.unavailable(w, share.Token)
		return
	}
	target, err := p.site.fileTarget(ctx, base, item, r.UserAgent())
	if err != nil {
		p.unavailable(w, share.Token)
		return
	}
	// Recheck expiration, revocation and code changes after the provider request.
	if _, ok := p.usable(w, r, http.StatusForbidden); !ok {
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusFound)
}

func shareSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	amount := float64(size)
	for _, unit := range []string{"KB", "MB", "GB", "TB"} {
		amount /= 1024
		if amount < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", amount, unit)
		}
	}
	return ""
}
