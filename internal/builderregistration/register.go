// Package builderregistration enrolls a builder without exporting local execution settings.
package builderregistration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Options struct{ Config, Server, HostID, HostSecret, Token, Name, APIURL, TokenOwner, CAFile string }
type Result struct {
	BuilderID string `json:"builder_id"`
	Secret    string `json:"secret"`
}

func Register(ctx context.Context, o Options) (Result, error) {
	base, err := url.Parse(o.Server)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && !(base.Scheme == "http" && loopback(base.Hostname()))) {
		return Result{}, errors.New("Dymmer requires HTTPS (HTTP is allowed only on loopback)")
	}
	var owner *[2]int
	if o.TokenOwner != "" {
		u, e := user.Lookup(o.TokenOwner)
		if e != nil {
			return Result{}, errors.New("unknown token owner")
		}
		uid, e := strconv.Atoi(u.Uid)
		if e != nil {
			return Result{}, e
		}
		gid, e := strconv.Atoi(u.Gid)
		if e != nil {
			return Result{}, e
		}
		owner = &[2]int{uid, gid}
	}
	path, err := filepath.Abs(o.Config)
	if err != nil {
		return Result{}, err
	}
	info, err := regular(path)
	if err != nil {
		return Result{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err = dec.Decode(&doc); err != nil {
		return Result{}, errors.New("invalid dymploy YAML")
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return Result{}, errors.New("expected one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return Result{}, errors.New("expected YAML mapping")
	}
	root := doc.Content[0]
	// Decode once as well to reject duplicate keys.
	var config map[string]any
	if err = root.Decode(&config); err != nil {
		return Result{}, errors.New("invalid or duplicate configuration keys")
	}
	if config["include"] != nil || config["includes"] != nil {
		return Result{}, errors.New("use a standalone dymploy configuration")
	}
	cp := mapping(root, "control_plane")
	if cp.Kind != yaml.MappingNode {
		return Result{}, errors.New("control_plane must be a mapping")
	}
	tokenPath := scalar(cp, "token_file")
	if tokenPath == "" {
		tokenPath = path + ".dymmer-token"
	} else if !filepath.IsAbs(tokenPath) {
		return Result{}, errors.New("control_plane.token_file must be absolute")
	}
	if filepath.Clean(tokenPath) == path {
		return Result{}, errors.New("token file cannot be the configuration file")
	}
	existing := ""
	if tokenInfo, tokenErr := regular(tokenPath); tokenErr == nil {
		if tokenInfo.Mode().Perm()&0077 != 0 {
			return Result{}, errors.New("existing profile secret must have mode 0600")
		}
		data, e := os.ReadFile(tokenPath)
		if e != nil {
			return Result{}, e
		}
		existing = strings.TrimSpace(string(data))
		if len(existing) != 43 {
			return Result{}, errors.New("invalid existing profile secret; no changes made")
		}
	} else if !errors.Is(tokenErr, os.ErrNotExist) {
		return Result{}, tokenErr
	}
	// Check both destinations before asking the server to issue a credential.
	for _, dest := range []string{path, tokenPath} {
		f, e := os.CreateTemp(filepath.Dir(dest), ".dym-register-check-*")
		if e != nil {
			return Result{}, e
		}
		f.Close()
		os.Remove(f.Name())
	}
	endpoint := *base
	endpoint.Path = strings.TrimRight(base.Path, "/") + "/api/v1/hosts/builder"
	socket := *base
	socket.Path = strings.TrimRight(base.Path, "/") + "/builders/websocket"
	socket.Scheme = "wss"
	if base.Scheme == "http" {
		socket.Scheme = "ws"
	}
	if current := scalar(cp, "url"); current != "" && current != socket.String() {
		return Result{}, errors.New("configuration already belongs to a different Dymmer endpoint")
	}
	name := o.Name
	if name == "" {
		name = "dymploy"
	}
	apiURL := o.APIURL
	if apiURL == "" {
		listen, _ := config["listen"].(string)
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		h, p, e := net.SplitHostPort(listen)
		if e != nil {
			return Result{}, errors.New("invalid dymploy listen address")
		}
		if h == "" || h == "0.0.0.0" || h == "::" {
			h = "127.0.0.1"
		}
		apiURL = "http://" + net.JoinHostPort(h, p)
	}
	policy, err := profilePolicy(config["policy"])
	if err != nil {
		return Result{}, err
	}
	payload, _ := json.Marshal(map[string]any{"name": name, "api_url": apiURL, "configuration": map[string]any{"policy": policy}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("X-Dym-Host", o.HostID)
	req.Header.Set("X-Dym-Secret", o.HostSecret)
	req.Header.Set("Content-Type", "application/json")
	if existing != "" {
		req.Header.Set("X-Dym-Builder-Secret", existing)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if o.CAFile != "" {
		cert, e := os.ReadFile(o.CAFile)
		if e != nil {
			return Result{}, e
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			return Result{}, e
		}
		if !roots.AppendCertsFromPEM(cert) {
			return Result{}, errors.New("invalid CA certificate")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		client.Transport = transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("registration connection failed; no local configuration changed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Result{}, fmt.Errorf("registration refused (HTTP %d); no local configuration changed", resp.StatusCode)
	}
	var result Result
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result); err != nil || result.BuilderID == "" {
		return Result{}, errors.New("invalid registration response")
	}
	if current := scalar(cp, "builder_id"); current != "" && current != result.BuilderID {
		return Result{}, errors.New("builder identity changed; refusing to overwrite configuration")
	}
	if result.Secret != "" {
		if len(result.Secret) != 43 {
			return Result{}, errors.New("invalid credential response")
		}
		if existing != "" {
			return Result{}, errors.New("unexpected credential rotation")
		}
		if err = atomicWrite(tokenPath, []byte(result.Secret+"\n"), 0600, info, owner); err != nil {
			return Result{}, errors.New("profile created but credential could not be saved; explicit credential recovery is required")
		}
	} else if existing == "" {
		return Result{}, errors.New("missing profile credential")
	}
	set(cp, "url", socket.String())
	set(cp, "builder_id", result.BuilderID)
	set(cp, "token_file", tokenPath)
	output, err := yaml.Marshal(&doc)
	if err != nil {
		return Result{}, err
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(current, raw) {
		return Result{}, errors.New("credential saved; configuration changed concurrently, rerun to reconcile")
	}
	if err = atomicWrite(path, output, info.Mode().Perm(), info, nil); err != nil {
		return Result{}, errors.New("credential saved, but configuration update failed; rerun the same command to recover")
	}
	result.Secret = ""
	return result, nil
}

func loopback(host string) bool {
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
func regular(path string) (os.FileInfo, error) {
	i, e := os.Lstat(path)
	if e == nil && !i.Mode().IsRegular() {
		return nil, errors.New("configuration and credential paths must be regular files, not symlinks")
	}
	return i, e
}
func scalar(n *yaml.Node, key string) string {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1].Value
		}
	}
	return ""
}
func mapping(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}
func set(n *yaml.Node, key, value string) {
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}
func profilePolicy(raw any) ([]map[string]any, error) {
	out := []map[string]any{}
	if raw == nil {
		return out, nil
	}
	rules, ok := raw.([]any)
	if !ok {
		return nil, errors.New("policy must be a list")
	}
	for _, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			return nil, errors.New("invalid policy rule")
		}
		item := map[string]any{}
		for _, k := range []string{"provider", "owner", "repositories", "branches", "tags"} {
			if v, exists := rule[k]; exists && v != nil {
				item[k] = v
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func atomicWrite(path string, data []byte, mode os.FileMode, original os.FileInfo, owner *[2]int) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dym-register-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if original != nil {
		if err = preserveOwner(f, original); err != nil {
			return err
		}
	}
	if owner != nil {
		if err = f.Chown(owner[0], owner[1]); err != nil {
			return err
		}
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
