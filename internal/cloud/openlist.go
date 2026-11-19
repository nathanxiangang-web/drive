package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Links struct {
	Connection *Connection
	Hosts      map[string]bool
}

var metadataHTTP = &http.Client{Timeout: 12 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (a Links) Resolve(ctx context.Context, cloudPath string, expected Item, userAgent string) (string, error) {
	if len(userAgent) > 2048 || strings.ContainsAny(userAgent, "\x00\r\n") {
		return "", Unavailable
	}
	token, root, err := a.Connection.Token(ctx)
	if err != nil {
		return "", Unavailable
	}
	cloudPath = path.Join(root, cloudPath)
	body, _ := json.Marshal(map[string]string{"path": cloudPath, "password": ""})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Connection.base+"/api/fs/get", bytes.NewReader(body))
	if err != nil {
		return "", Unavailable
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Content-Type", "application/json")
	response, err := metadataHTTP.Do(request)
	if err != nil {
		return "", Unavailable
	}
	defer response.Body.Close()
	var data struct {
		Code int `json:"code"`
		Data struct {
			URL       string `json:"raw_url"`
			Name      string `json:"name"`
			Size      *int64 `json:"size"`
			Directory *bool  `json:"is_dir"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data)
	if response.StatusCode == http.StatusUnauthorized || data.Code == 401 {
		a.Connection.Expired(token)
	}
	if response.StatusCode != http.StatusOK || decodeErr != nil || data.Code != 200 || data.Data.Name != expected.Name || data.Data.Size == nil || *data.Data.Size != expected.Size || data.Data.Directory == nil || *data.Data.Directory {
		return "", Unavailable
	}
	return checkedLink(data.Data.URL, a.Hosts)
}

func checkedLink(raw string, hosts map[string]bool) (string, error) {
	target, err := url.Parse(raw)
	if err != nil || len(raw) > 16384 || strings.ContainsAny(raw, "#\x00\r\n") || target.Scheme != "https" || target.User != nil || target.Opaque != "" || (!hosts[target.Host] && !(target.Port() == "443" && hosts[target.Hostname()])) {
		return "", Unavailable
	}
	return target.String(), nil
}
