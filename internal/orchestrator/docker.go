package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type DockerEngine struct {
	HTTP *http.Client
}

func NewDockerEngine() (DockerEngine, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}
	return DockerEngine{HTTP: &http.Client{Transport: transport}}, nil
}

func (d DockerEngine) ImageID(ctx context.Context, ref string) (string, error) {
	var info struct {
		ID string `json:"Id"`
	}
	if err := d.json(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, &info); err != nil {
		return "", err
	}
	return info.ID, nil
}

func (d DockerEngine) Start(ctx context.Context, spec ContainerSpec) error {
	if _, err := d.Inspect(ctx, spec.Name); err == nil {
		_ = d.Stop(ctx, spec.Name)
	}
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	body := map[string]any{
		"Image":  spec.Image,
		"Cmd":    spec.Command,
		"Env":    env,
		"Labels": spec.Labels,
		"HostConfig": map[string]any{
			"NetworkMode": spec.Network,
			"IpcMode":     spec.Ipc,
			"Binds":       spec.Binds,
			"AutoRemove":  false,
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.json(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(spec.Name), body, &created); err != nil {
		return fmt.Errorf("create %s: %w", spec.Name, err)
	}
	if err := d.json(ctx, http.MethodPost, "/containers/"+created.ID+"/start", map[string]any{}, nil); err != nil {
		return fmt.Errorf("start %s: %w", spec.Name, err)
	}
	return nil
}

func (d DockerEngine) Stop(ctx context.Context, name string) error {
	err := d.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop?t=20", nil, nil)
	if err != nil && !isNotFound(err) {
		return err
	}
	err = d.json(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil, nil)
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (d DockerEngine) Inspect(ctx context.Context, name string) (ContainerInfo, error) {
	var raw struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		Image string `json:"Image"`
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	if err := d.json(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &raw); err != nil {
		if isNotFound(err) {
			return ContainerInfo{}, ErrNotFound
		}
		return ContainerInfo{}, err
	}
	return ContainerInfo{
		Name:    strings.TrimPrefix(raw.Name, "/"),
		ID:      raw.ID,
		Image:   raw.Config.Image,
		Running: raw.State.Running,
	}, nil
}

func (d DockerEngine) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/exec", map[string]any{
		"Cmd":          cmd,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          true,
	}, &created); err != nil {
		return "", err
	}
	raw, err := d.raw(ctx, http.MethodPost, "/exec/"+created.ID+"/start", map[string]any{"Detach": false, "Tty": true})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stripDockerStream(raw)), nil
}

func (d DockerEngine) List(ctx context.Context) ([]ContainerInfo, error) {
	query := url.Values{}
	query.Set("all", "true")
	query.Set("filters", fmt.Sprintf(`{"label":["%s=true"]}`, contract.LabelManaged))
	var items []struct {
		ID     string   `json:"Id"`
		Names  []string `json:"Names"`
		Image  string   `json:"Image"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
	}
	if err := d.json(ctx, http.MethodGet, "/containers/json?"+query.Encode(), nil, &items); err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(items))
	for _, item := range items {
		name := item.ID
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		out = append(out, ContainerInfo{Name: name, ID: item.ID, Image: item.Image, Running: item.State == "running"})
	}
	return out, nil
}

func (d DockerEngine) json(ctx context.Context, method, path string, body any, dest any) error {
	raw, err := d.raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if dest == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dest)
}

func (d DockerEngine) raw(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return payload, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode >= 300 {
		return payload, fmt.Errorf("docker %s %s: %s", method, path, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrNotFound.Error())
}

func stripDockerStream(raw []byte) string {
	if len(raw) >= 8 && raw[0] <= 2 {
		var out strings.Builder
		for i := 0; i+8 <= len(raw); {
			size := int(raw[i+4])<<24 | int(raw[i+5])<<16 | int(raw[i+6])<<8 | int(raw[i+7])
			i += 8
			if i+size > len(raw) {
				out.Write(raw[i:])
				break
			}
			out.Write(raw[i : i+size])
			i += size
		}
		return out.String()
	}
	return string(raw)
}
