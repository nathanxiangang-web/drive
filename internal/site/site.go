package site

import (
	"context"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/download"
	"github.com/nathanxiangang-web/drive/internal/store"
	"golang.org/x/time/rate"
)

//go:embed web/*
var assets embed.FS

const cookieName = "__Host-drive"

var operationPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{20,64}$`)

type Site struct {
	OpenRegistration                            bool
	registrationLimit                           *rate.Limiter
	Store                                       *store.Store
	Drive                                       *cloud.Native
	Links                                       cloud.Links
	Origin, PublicRoot, PrivateRoot, LegacyRoot string
	searchKey                                   []byte
	Connection                                  *cloud.Connection
	Started                                     time.Time
	configurationLimit                          *rate.Limiter
	DownloadEnabled                             bool
	loginLimit                                  *rate.Limiter
}

func (s *Site) Routes() http.Handler {
	s.searchKey, _ = hex.DecodeString(store.Random())
	s.loginLimit = rate.NewLimiter(rate.Every(2*time.Second), 5)
	s.registrationLimit = rate.NewLimiter(rate.Every(5*time.Second), 5)
	s.configurationLimit = rate.NewLimiter(rate.Every(5*time.Second), 2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/session", s.auth(s.session))
	mux.HandleFunc("GET /api/admin/status", s.admin(s.adminStatus))
	mux.HandleFunc("GET /api/admin/users", s.admin(s.adminUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{login}", s.admin(s.changeUser))
	mux.HandleFunc("DELETE /api/admin/users/{login}", s.admin(s.deleteUser))
	mux.HandleFunc("GET /api/admin/shares", s.admin(s.adminShares))
	mux.HandleFunc("PATCH /api/admin/shares/{token}", s.admin(s.adminShareCode))
	mux.HandleFunc("DELETE /api/admin/shares/{token}", s.admin(s.adminRevokeShare))
	mux.HandleFunc("PUT /api/admin/openlist", s.admin(s.configureOpenList))
	mux.HandleFunc("GET /api/files", s.auth(s.files))
	mux.HandleFunc("GET /api/file", s.auth(s.file))
	mux.HandleFunc("POST /api/shares", s.auth(s.createShare))
	mux.HandleFunc("GET /api/shares", s.auth(s.listShares))
	mux.HandleFunc("GET /api/shares/mine", s.auth(s.myShares))
	mux.HandleFunc("GET /api/shares/code", s.auth(s.newShareCode))
	mux.HandleFunc("DELETE /api/shares/{token}", s.auth(s.revokeShare))
	mux.HandleFunc("GET /api/tasks", s.auth(s.tasks))
	mux.HandleFunc("POST /api/tasks", s.auth(s.start))
	mux.HandleFunc("DELETE /api/tasks", s.auth(s.clearTasks))
	mux.HandleFunc("DELETE /api/tasks/{id}", s.auth(s.clearTask))
	s.webRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		media := []string{"'self'"}
		for host := range s.Links.Hosts {
			media = append(media, "https://"+host)
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; media-src "+strings.Join(media, " "))
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != s.Origin {
			problem(w, 403, "请求来源无效")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Site) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		problem(w, 503, "服务暂不可用")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "CloudDrive OK\n")
}

func respond(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
func problem(w http.ResponseWriter, code int, message string) {
	respond(w, code, struct {
		Error string `json:"error"`
	}{message})
}

func body(w http.ResponseWriter, r *http.Request, target any) bool {
	return bodyWithLimit(w, r, target, 70<<10)
}

func bodyWithLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; media != "application/json" {
		problem(w, 415, "请求格式无效")
		return false
	}
	decode := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decode.DisallowUnknownFields()
	if decode.Decode(target) != nil || decode.Decode(new(any)) != io.EOF {
		problem(w, 400, "请求格式无效")
		return false
	}
	return true
}

func (s *Site) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow() {
		problem(w, 429, "登录频繁，请稍后重试")
		return
	}
	var input struct{ Login, Password string }
	if !body(w, r, &input) {
		return
	}
	token, err := s.Store.Authenticate(r.Context(), strings.TrimSpace(input.Login), input.Password)
	if errors.Is(err, store.Invalid) {
		problem(w, 401, "账号或密码无效")
		return
	}
	if err != nil {
		problem(w, 503, "暂时无法登录，请稍后重试")
		return
	}
	sessionCookie(w, token)
	w.WriteHeader(http.StatusNoContent)
}

func sessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
}

func (s *Site) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		if err := s.Store.Logout(r.Context(), cookie.Value); err != nil {
			problem(w, 503, "退出失败，请重试")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Site) auth(handler func(http.ResponseWriter, *http.Request, store.Account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			problem(w, 401, "请登录")
			return
		}
		account, err := s.Store.Account(r.Context(), cookie.Value)
		if errors.Is(err, store.Invalid) || errors.Is(err, pgx.ErrNoRows) {
			problem(w, 401, "请登录")
			return
		}
		if err != nil {
			problem(w, 503, "暂时无法读取登录状态，请稍后重试")
			return
		}
		handler(w, r, account)
	}
}

func (s *Site) session(w http.ResponseWriter, r *http.Request, a store.Account) {
	respond(w, 200, struct {
		Login     string `json:"login"`
		Downloads bool   `json:"downloads"`
		Role      string `json:"role"`
		Setup     bool   `json:"setup"`
	}{a.Login, s.DownloadEnabled && a.CanDownload(), a.Role, a.Role == store.Admin && s.Connection.Info().Mode == "unconfigured"})
}

func normalize(relative string) (string, error) {
	if relative == "" {
		return "", nil
	}
	if len(relative) > 2048 || strings.HasPrefix(relative, "/") {
		return "", cloud.Missing
	}
	for _, part := range strings.Split(relative, "/") {
		if !cloud.ValidName(part) {
			return "", cloud.Missing
		}
	}
	return relative, nil
}

func (s *Site) publicRestricted(first string) bool {
	return strings.EqualFold(first, path.Base(s.PrivateRoot)) || strings.EqualFold(first, path.Base(s.LegacyRoot)) || strings.EqualFold(first, "云下载")
}

func (s *Site) directory(ctx context.Context, a store.Account, area, relative string) (string, error) {
	relative, err := normalize(relative)
	if err != nil {
		return "", err
	}
	switch area {
	case "", "public":
		first, _, _ := strings.Cut(relative, "/")
		if s.publicRestricted(first) {
			return "", cloud.Missing
		}
		return path.Join(s.PublicRoot, relative), nil
	case "private":
		return s.privateDirectory(ctx, a, relative)
	}
	return "", cloud.Missing
}

func (s *Site) visible(area, relative string, item cloud.Item) bool {
	return !(area != "private" && relative == "" && s.publicRestricted(item.Name))
}

func offset(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 2147483647 {
		return 0, cloud.Missing
	}
	return n, nil
}

func (s *Site) files(w http.ResponseWriter, r *http.Request, a store.Account) {
	q := r.URL.Query()
	area, relative := q.Get("area"), q.Get("dir")
	ctx, cancel := context.WithTimeout(r.Context(), 23*time.Second)
	defer cancel()
	n, err := offset(q.Get("offset"))
	if err != nil || (n >= sourceStride && (area != "private" || relative != "")) {
		fileFailure(w, cloud.Missing)
		return
	}
	var page entryPage
	refresh := q.Get("refresh") == "1"
	switch {
	case q.Has("q"):
		page, err = s.searchPage(ctx, a, area, relative, q.Get("q"), n)
	case area == "private" && relative == "":
		page, err = s.privateRoot(ctx, a, n, refresh)
	default:
		var base string
		base, err = s.directory(ctx, a, area, relative)
		if err == nil {
			page, err = s.listPage(ctx, area, relative, base, n, refresh)
		}
	}
	if errors.Is(err, invalidSearch) {
		problem(w, 400, err.Error())
		return
	}
	if err != nil {
		fileFailure(w, err)
		return
	}
	page.Offset = n
	respond(w, 200, page)
}

type fileSelection struct {
	Item                    cloud.Item
	Area, Dir, Base, Parent string
}

func (s *Site) selectFile(ctx context.Context, a store.Account, q url.Values) (fileSelection, error) {
	if token := q.Get("ref"); token != "" {
		ref, err := s.parseSearch(token, a.ID)
		if err != nil || ref.Item.Directory || !s.searchable(ref.Area, ref.Dir, ref.Item) {
			return fileSelection{}, cloud.Missing
		}
		base, err := s.directory(ctx, a, ref.Area, ref.Dir)
		if err != nil {
			return fileSelection{}, err
		}
		page, err := s.Drive.List(ctx, base, 0, false)
		if err != nil {
			return fileSelection{}, err
		}
		if page.CID != ref.Parent {
			return fileSelection{}, cloud.Missing
		}
		area := ref.Area
		if area == "" {
			area = "public"
		}
		return fileSelection{ref.Item, area, ref.Dir, base, page.CID}, nil
	}
	area, relative := q.Get("area"), q.Get("dir")
	if area == "" {
		area = "public"
	}
	n, err := offset(q.Get("offset"))
	if err != nil || n >= sourceStride {
		return fileSelection{}, cloud.Missing
	}
	base, err := s.directory(ctx, a, area, relative)
	if err != nil {
		return fileSelection{}, err
	}
	page, err := s.Drive.List(ctx, base, n, false)
	if err != nil {
		return fileSelection{}, err
	}
	for _, item := range page.Items {
		if item.ID == q.Get("id") && !item.Directory && s.visible(area, relative, item) {
			return fileSelection{item, area, relative, base, page.CID}, nil
		}
	}
	return fileSelection{}, cloud.Missing
}

func (s *Site) file(w http.ResponseWriter, r *http.Request, a store.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), 22*time.Second)
	defer cancel()
	selected, err := s.selectFile(ctx, a, r.URL.Query())
	if err != nil {
		fileFailure(w, err)
		return
	}
	target, err := s.fileTarget(ctx, selected.Base, selected.Item, r.UserAgent())
	if err != nil {
		fileFailure(w, err)
		return
	}
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusFound)
}

func (s *Site) fileTarget(ctx context.Context, base string, item cloud.Item, userAgent string) (string, error) {
	absolute := path.Join(base, item.Name)
	if !strings.HasPrefix(absolute, s.PublicRoot+"/") {
		return "", cloud.Missing
	}
	return s.Links.Resolve(ctx, strings.TrimPrefix(absolute, s.PublicRoot), item, userAgent)
}

func fileFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, cloud.Missing) {
		problem(w, 404, "文件或目录不存在")
		return
	}
	problem(w, 503, "网盘暂未返回可用结果，请稍后重试")
}

func (s *Site) tasks(w http.ResponseWriter, r *http.Request, a store.Account) {
	items, err := s.Store.ListJobs(r.Context(), a.ID)
	if err != nil {
		problem(w, 503, "任务列表暂不可用")
		return
	}
	respond(w, 200, items)
}

func (s *Site) start(w http.ResponseWriter, r *http.Request, a store.Account) {
	if !a.CanDownload() {
		problem(w, 403, "此账号仅可浏览和下载现有文件")
		return
	}
	if !s.DownloadEnabled {
		problem(w, 503, "云下载暂不可用")
		return
	}
	var input struct {
		Source string `json:"source"`
		Key    string `json:"operation_key"`
	}
	if !bodyWithLimit(w, r, &input, 1<<20) {
		return
	}
	if !operationPattern.MatchString(input.Key) {
		problem(w, 400, "提交编号无效")
		return
	}
	prepared, _, err := download.Prepare(input.Source)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	job, err := s.Store.Submit(r.Context(), a.ID, input.Key, prepared, prepared)
	if errors.Is(err, store.Conflict) {
		problem(w, 409, "提交编号已被其他链接使用")
		return
	}
	if err != nil {
		problem(w, 503, "暂时无法保存任务，请重试")
		return
	}
	respond(w, 202, job)
}
