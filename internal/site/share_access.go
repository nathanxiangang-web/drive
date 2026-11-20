package site

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nathanxiangang-web/drive/internal/store"
)

const shareAccessCookie = "__Secure-drive-share"
const shareAccessAge = 12 * time.Hour

func (p sharedPages) accessValue(token string, version, expires int64) string {
	ts := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, p.site.searchKey)
	mac.Write([]byte("share-access:" + token + ":" + strconv.FormatInt(version, 10) + ":" + ts))
	return ts + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (p sharedPages) hasAccess(r *http.Request, share store.Share) bool {
	if share.CodeVersion == 0 {
		return true
	}
	cookie, err := r.Cookie(shareAccessCookie)
	if err != nil || len(cookie.Value) > 64 {
		return false
	}
	cut, _, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}
	expires, err := strconv.ParseInt(cut, 10, 64)
	return err == nil && expires > time.Now().Unix() &&
		hmac.Equal([]byte(cookie.Value), []byte(p.accessValue(share.Token, share.CodeVersion, expires)))
}

func (p sharedPages) locked(w http.ResponseWriter, token string, status int, message string) {
	// Keep the same-origin form POST's Origin header without sending
	// the share address to an external origin.
	w.Header().Set("Referrer-Policy", "same-origin")
	p.render(w, status, shareView{Unlock: "/s/" + token + "/unlock", Message: message})
}

func (p sharedPages) unlock(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !shareToken.MatchString(token) {
		p.invalid(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil {
		p.locked(w, token, 400, store.InvalidShareCode.Error())
		return
	}
	version, err := p.site.Store.UnlockShare(r.Context(), token, r.PostForm.Get("code"))
	switch {
	case errors.Is(err, store.ShareMissing):
		p.invalid(w)
	case errors.Is(err, store.InvalidShareCode):
		p.locked(w, token, 400, err.Error())
	case errors.Is(err, store.WrongShareCode):
		p.locked(w, token, 403, err.Error())
	case errors.Is(err, store.ShareCodeLimited):
		p.locked(w, token, 429, err.Error())
	case err != nil:
		p.unavailable(w, token)
	default:
		url := "/s/" + token
		if version != 0 {
			expires := time.Now().Add(shareAccessAge)
			http.SetCookie(w, &http.Cookie{Name: shareAccessCookie, Path: url, Secure: true, HttpOnly: true,
				SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(shareAccessAge.Seconds()),
				Value: p.accessValue(token, version, expires.Unix())})
		}
		http.Redirect(w, r, url, http.StatusSeeOther)
	}
}
