package cloud

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var InvalidConnection = errors.New("OpenList 账号、密码或文件根目录验证失败")
var ConnectionKeyMissing = errors.New("服务器尚未配置 OpenList 加密密钥")

type ConnectionStore interface {
	LoadOpenList(context.Context) ([]byte, error)
	SaveOpenList(context.Context, string, []byte) error
}

type OpenListCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Root     string `json:"root"`
}

type ConnectionInfo struct {
	Mode     string `json:"mode"`
	Username string `json:"username,omitempty"`
	Root     string `json:"root"`
	Editable bool   `json:"editable"`
}

// Connection holds one login per app instance. Reauthentication is bounded
// even when OpenList rejects a previously valid password.
type Connection struct {
	base, tokenFile, root string
	store                 ConnectionStore
	key                   cipher.AEAD
	mu                    sync.Mutex
	credentials           *OpenListCredentials
	token                 string
	reauthAt              time.Time
}

func OpenConnection(ctx context.Context, db ConnectionStore, base, tokenFile, root, keyFile string) (*Connection, error) {
	c := &Connection{base: strings.TrimRight(base, "/"), tokenFile: tokenFile, root: root, store: db}
	raw, err := os.ReadFile(keyFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ConnectionKeyMissing
	}
	if err == nil {
		defer clear(raw)
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, ConnectionKeyMissing
		}
		block, err := aes.NewCipher(key)
		clear(key)
		if err != nil {
			return nil, ConnectionKeyMissing
		}
		c.key, err = cipher.NewGCM(block)
		if err != nil {
			return nil, ConnectionKeyMissing
		}
	}
	sealed, err := db.LoadOpenList(ctx)
	if err != nil {
		return nil, err
	}
	if len(sealed) == 0 {
		return c, nil
	}
	if c.key == nil || len(sealed) <= c.key.NonceSize() {
		return nil, ConnectionKeyMissing
	}
	n := c.key.NonceSize()
	body, err := c.key.Open(nil, sealed[:n], sealed[n:], []byte("cloudsite-drive/openlist/v1"))
	if err != nil {
		return nil, ConnectionKeyMissing
	}
	defer clear(body)
	c.credentials = &OpenListCredentials{}
	if json.Unmarshal(body, c.credentials) != nil {
		return nil, ConnectionKeyMissing
	}
	return c, nil
}

func (c *Connection) Info() ConnectionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.credentials != nil {
		return ConnectionInfo{"account", c.credentials.Username, c.credentials.Root, c.key != nil}
	}
	mode := "unconfigured"
	if file, err := os.Stat(c.tokenFile); err == nil && file.Mode().IsRegular() && file.Size() > 0 {
		mode = "token"
	}
	return ConnectionInfo{Mode: mode, Root: c.root, Editable: c.key != nil}
}

func (c *Connection) Ping(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/ping", nil)
	if err != nil {
		return false
	}
	res, err := metadataHTTP.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func validRoot(root string) bool {
	if root == "/" {
		return true
	}
	if !strings.HasPrefix(root, "/") || len(root) > 2048 || path.Clean(root) != root {
		return false
	}
	for _, part := range strings.Split(root[1:], "/") {
		if !ValidName(part) {
			return false
		}
	}
	return true
}

func (c *Connection) Configure(ctx context.Context, actor string, input OpenListCredentials) error {
	if c.key == nil {
		return ConnectionKeyMissing
	}
	input.Username = strings.TrimSpace(input.Username)
	input.Root = strings.TrimSpace(input.Root)
	if input.Root == "" {
		input.Root = "/"
	}
	if len(input.Username) == 0 || len(input.Username) > 128 || len(input.Password) == 0 || len(input.Password) > 1024 || !validRoot(input.Root) {
		return InvalidConnection
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	token, err := c.login(ctx, input)
	if err != nil {
		return err
	}
	var list struct {
		Code int `json:"code"`
	}
	payload := map[string]any{"path": input.Root, "per_page": 1, "page": 1}
	if c.call(ctx, "/api/fs/list", payload, token, &list) != nil || list.Code != 200 {
		return InvalidConnection
	}
	plain, err := json.Marshal(input)
	if err != nil {
		return err
	}
	defer clear(plain)
	nonce := make([]byte, c.key.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := c.key.Seal(nonce, nonce, plain, []byte("cloudsite-drive/openlist/v1"))
	if err = c.store.SaveOpenList(ctx, actor, sealed); err != nil {
		return err
	}
	c.credentials = &input
	c.token = token
	c.reauthAt = time.Time{}
	return nil
}

func (c *Connection) Token(ctx context.Context) (token, root string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.credentials == nil {
		value, err := os.ReadFile(c.tokenFile)
		if err != nil || len(value) > 8192 {
			return "", "", Unavailable
		}
		defer clear(value)
		token := strings.TrimSpace(string(value))
		if !validToken(token) {
			return "", "", Unavailable
		}
		return token, c.root, nil
	}
	if c.token == "" {
		if time.Now().Before(c.reauthAt) {
			return "", "", Unavailable
		}
		c.reauthAt = time.Now().Add(30 * time.Second)
		value, err := c.login(ctx, *c.credentials)
		if err != nil {
			return "", "", err
		}
		c.token = value
	}
	return c.token, c.credentials.Root, nil
}

func (c *Connection) Expired(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == token {
		c.token = ""
	}
}

func validToken(token string) bool {
	return token != "" && len(token) <= 8192 && !strings.ContainsAny(token, "\r\n\x00")
}

func (c *Connection) login(ctx context.Context, credential OpenListCredentials) (string, error) {
	var result struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	payload := map[string]string{"username": credential.Username, "password": credential.Password}
	if c.call(ctx, "/api/auth/login", payload, "", &result) != nil || result.Code != 200 || !validToken(result.Data.Token) {
		return "", InvalidConnection
	}
	return result.Data.Token, nil
}

func (c *Connection) call(ctx context.Context, endpoint string, payload any, token string, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return InvalidConnection
	}
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+endpoint, bytes.NewReader(body))
	if err != nil {
		return InvalidConnection
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	res, err := metadataHTTP.Do(req)
	if err != nil {
		return InvalidConnection
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return InvalidConnection
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(target)
}
