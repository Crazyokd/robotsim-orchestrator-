package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 会话容器的发现约定：所有由 simenv 起的容器都打 LabelManaged，
// 世界与车端再用 LabelRole 区分角色。它们只属于容器运行时层——
// 不进会话契约，框架与 CLI 不认识容器。
const (
	LabelManaged = "simenv.managed"
	LabelRole    = "simenv.role"
	RoleWorld    = "world"
	RoleApp      = "app"
)

var ErrNotFound = errors.New("container not found")

// hostGPUDevice 是宿主 GPU 设备目录。
const hostGPUDevice = "/dev/dri"

// drmSysfs 列出 DRM 节点。测试替换它，避免读到跑测试的那台机器。
var drmSysfs = "/sys/class/drm"

const nvidiaVendor = "0x10de"

// nvidiaRuntimePath 是 NVIDIA 用户态驱动的注入入口。镜像里没有这份驱动，
// 只挂 NVIDIA 的 DRM 节点会让 ogre2 在 eglInitialize 失败后退出。
var nvidiaRuntimePath = "/usr/bin/nvidia-container-runtime"

// GPU 是一次容器该使用的显示设备。Runtime 为空表示普通 runc。
type GPU struct {
	Devices []string
	Runtime string
}

// HostGPU 选择容器的显示设备。有 NVIDIA 节点且宿主能注入其驱动时只用 NVIDIA，
// 不把核显一起挂进去。没有这份驱动时不能挂 NVIDIA 节点，混合机器退回 Mesa 节点。
// 只有一种显卡或读不到厂商时挂整份 /dev/dri。
func HostGPU() GPU {
	if _, err := os.Stat(hostGPUDevice); err != nil {
		return GPU{}
	}
	mesa, nvidia := drmByVendor(drmSysfs)
	devices, runtime := chooseGPU(mesa, nvidia, nvidiaRuntimePresent())
	if devices == nil {
		return GPU{Devices: []string{hostGPUDevice}}
	}
	return GPU{Devices: devices, Runtime: runtime}
}

// HostGPUDevices 是 HostGPU 的设备列表。没有 /dev/dri 时为空。
func HostGPUDevices() []string {
	return HostGPU().Devices
}

func nvidiaRuntimePresent() bool {
	_, err := os.Stat(nvidiaRuntimePath)
	return err == nil
}

// chooseGPU 在调用方已经确认 /dev/dri 存在时选择节点。devices 为 nil 表示挂整份目录。
func chooseGPU(mesa, nvidia []string, nvidiaRuntime bool) (devices []string, runtime string) {
	if len(nvidia) > 0 && nvidiaRuntime {
		sort.Strings(nvidia)
		return nvidia, "nvidia"
	}
	if len(mesa) > 0 && len(nvidia) > 0 {
		sort.Strings(mesa)
		return mesa, ""
	}
	return nil, ""
}

func drmByVendor(sysfs string) (mesa, nvidia []string) {
	entries, err := os.ReadDir(sysfs)
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !drmNode(name) {
			continue
		}
		vendor, err := os.ReadFile(filepath.Join(sysfs, name, "device", "vendor"))
		if err != nil {
			continue
		}
		path := filepath.Join(hostGPUDevice, name)
		if strings.EqualFold(strings.TrimSpace(string(vendor)), nvidiaVendor) {
			nvidia = append(nvidia, path)
			continue
		}
		mesa = append(mesa, path)
	}
	return mesa, nvidia
}

func appendRuntimeEnv(env []string, runtime string) []string {
	if runtime != "nvidia" {
		return env
	}
	if !envHasPrefix(env, "NVIDIA_VISIBLE_DEVICES=") {
		env = append(env, "NVIDIA_VISIBLE_DEVICES=all")
	}
	if !envHasPrefix(env, "NVIDIA_DRIVER_CAPABILITIES=") {
		env = append(env, "NVIDIA_DRIVER_CAPABILITIES=all")
	}
	return env
}

func envHasPrefix(env []string, prefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func drmNode(name string) bool {
	if strings.HasPrefix(name, "renderD") {
		_, err := strconv.Atoi(strings.TrimPrefix(name, "renderD"))
		return err == nil
	}
	if !strings.HasPrefix(name, "card") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimPrefix(name, "card"))
	return err == nil
}

type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
	Env     map[string]string
	Binds   []string
	// Archives are copied to container directories before its process starts.
	Archives map[string][]byte
	Network  string
	Ipc      string
	Devices  []string
	// Runtime 为空时用守护进程的默认运行时。NVIDIA 设备为 "nvidia"。
	Runtime string
	Labels  map[string]string
}

type ContainerInfo struct {
	Name    string
	ID      string
	Image   string
	Running bool
	Labels  map[string]string
}

// Engine 是容器运行时接口：需要容器的世界 Port / 车端 Port 经它起停容器。
// 接口与 Docker 实现同住一个包；测试用 fake 实现替换。
type Engine interface {
	ImageID(ctx context.Context, ref string) (string, error)
	// ReadImageFile reads one file out of an image without running anything.
	ReadImageFile(ctx context.Context, image, path string) ([]byte, error)
	ReadImageArchive(ctx context.Context, image, path string) ([]byte, error)
	Start(ctx context.Context, spec ContainerSpec) error
	Stop(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (ContainerInfo, error)
	Exec(ctx context.Context, name string, cmd []string) (string, error)
	List(ctx context.Context) ([]ContainerInfo, error)
}

type DockerEngine struct {
	HTTP *http.Client
}

var errContainerAlreadyStopped = errors.New("container already stopped")

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
		if err := d.Stop(ctx, spec.Name); err != nil {
			return fmt.Errorf("stop existing container %s: %w", spec.Name, err)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("inspect existing container %s: %w", spec.Name, err)
	}
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	env = appendRuntimeEnv(env, spec.Runtime)
	hostConfig := map[string]any{
		"NetworkMode": spec.Network,
		"IpcMode":     spec.Ipc,
		"Binds":       spec.Binds,
		"Devices":     deviceMappings(spec.Devices),
		"AutoRemove":  false,
	}
	if spec.Runtime != "" {
		hostConfig["Runtime"] = spec.Runtime
	}
	body := map[string]any{
		"Image":      spec.Image,
		"Cmd":        spec.Command,
		"Env":        env,
		"Labels":     spec.Labels,
		"HostConfig": hostConfig,
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.json(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(spec.Name), body, &created); err != nil {
		return fmt.Errorf("create %s: %w", spec.Name, err)
	}
	for directory, archive := range spec.Archives {
		query := url.Values{"path": []string{directory}}
		if _, err := d.raw(ctx, http.MethodPut, "/containers/"+created.ID+"/archive?"+query.Encode(), archive); err != nil {
			return fmt.Errorf("copy assets to %s:%s: %w", spec.Name, directory, errors.Join(err, d.removeCreatedContainer(created.ID)))
		}
	}
	if err := d.json(ctx, http.MethodPost, "/containers/"+created.ID+"/start", map[string]any{}, nil); err != nil {
		cleanupErr := d.removeCreatedContainer(created.ID)
		if cleanupErr != nil {
			return fmt.Errorf("start %s: %w", spec.Name, errors.Join(err, fmt.Errorf("remove created container: %w", cleanupErr)))
		}
		return fmt.Errorf("start %s: %w", spec.Name, err)
	}
	return nil
}

// ReadImageFile reads one file out of an image. Docker exposes no image-file
// API, so this creates a container that is never started, copies the file out
// of it the way `docker cp` does, and removes it. Nothing runs.
func (d DockerEngine) ReadImageFile(ctx context.Context, image, path string) ([]byte, error) {
	raw, err := d.ReadImageArchive(ctx, image, path)
	if err != nil {
		return nil, err
	}
	return firstArchiveFile(raw)
}

func (d DockerEngine) ReadImageArchive(ctx context.Context, image, path string) (raw []byte, err error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.json(ctx, http.MethodPost, "/containers/create", map[string]any{"Image": image}, &created); err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = errors.Join(err, d.deleteContainer(cleanupCtx, created.ID))
	}()
	query := url.Values{}
	query.Set("path", path)
	return d.raw(ctx, http.MethodGet, "/containers/"+created.ID+"/archive?"+query.Encode(), nil)
}

func firstArchiveFile(raw []byte) ([]byte, error) {
	reader := tar.NewReader(bytes.NewReader(raw))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archive holds no regular file")
		}
		if err != nil {
			return nil, err
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		return io.ReadAll(reader)
	}
}

func deviceMappings(devices []string) []map[string]string {
	out := make([]map[string]string, 0, len(devices))
	for _, dev := range devices {
		host, container, _ := strings.Cut(dev, ":")
		if container == "" {
			container = host
		}
		out = append(out, map[string]string{
			"PathOnHost":        host,
			"PathInContainer":   container,
			"CgroupPermissions": "rwm",
		})
	}
	return out
}

func (d DockerEngine) Stop(ctx context.Context, name string) error {
	err := d.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop?t=20", nil, nil)
	if err != nil && !isNotFound(err) && !errors.Is(err, errContainerAlreadyStopped) {
		return err
	}
	return d.deleteContainer(ctx, name)
}

func (d DockerEngine) deleteContainer(ctx context.Context, name string) error {
	err := d.json(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil, nil)
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (d DockerEngine) removeCreatedContainer(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.deleteContainer(ctx, name)
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
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
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
		Labels:  raw.Config.Labels,
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
	query.Set("filters", fmt.Sprintf(`{"label":["%s=true"]}`, LabelManaged))
	var items []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
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
		out = append(out, ContainerInfo{
			Name: name, ID: item.ID, Image: item.Image, Running: item.State == "running", Labels: item.Labels,
		})
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
	contentType := "application/json"
	if archive, ok := body.([]byte); ok {
		reader = bytes.NewReader(archive)
		contentType = "application/x-tar"
	} else if body != nil {
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
		req.Header.Set("Content-Type", contentType)
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
	if resp.StatusCode == http.StatusNotModified {
		return payload, errContainerAlreadyStopped
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
