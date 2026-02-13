package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	DefaultNetworkName = "vega-network"
	LabelProject       = "vega.project"
	LabelManagedBy     = "vega.managed-by"
	DefaultImage       = "node:20-slim"
	containerPrefix    = "vega-"
)

// Manager handles Docker container operations for projects.
type Manager struct {
	client      *client.Client
	baseDir     string
	networkName string
	defaultImg  string
	mu          sync.RWMutex
	available   bool
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithNetworkName sets a custom Docker network name.
func WithNetworkName(name string) ManagerOption {
	return func(m *Manager) {
		m.networkName = name
	}
}

// WithDefaultImage sets the default container image.
func WithDefaultImage(img string) ManagerOption {
	return func(m *Manager) {
		m.defaultImg = img
	}
}

// NewManager creates a new container manager.
// If Docker is unavailable, it returns a Manager with available=false.
func NewManager(baseDir string, opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		baseDir:     baseDir,
		networkName: DefaultNetworkName,
		defaultImg:  DefaultImage,
		available:   false,
	}

	for _, opt := range opts {
		opt(m)
	}

	// Try to create Docker client
	cli, err := createDockerClient()
	if err != nil {
		return m, nil
	}

	// Check if Docker is actually available
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	if err != nil {
		cli.Close()
		return m, nil
	}

	m.client = cli
	m.available = true

	// Ensure network exists
	if err := m.ensureNetwork(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return m, nil
}

// createDockerClient creates a Docker client, trying multiple socket locations
// for compatibility with Docker Desktop on macOS.
func createDockerClient() (*client.Client, error) {
	// First try with environment settings (DOCKER_HOST, etc.)
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := cli.Ping(ctx); err == nil {
			return cli, nil
		}
		cli.Close()
	}

	// Try common Docker Desktop socket locations
	socketPaths := []string{
		"unix://" + os.Getenv("HOME") + "/.docker/run/docker.sock", // Docker Desktop macOS
		"unix:///var/run/docker.sock",                               // Linux default
		"unix://" + os.Getenv("HOME") + "/.colima/docker.sock",     // Colima
	}

	for _, socketPath := range socketPaths {
		cli, err := client.NewClientWithOpts(
			client.WithHost(socketPath),
			client.WithAPIVersionNegotiation(),
		)
		if err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err = cli.Ping(ctx)
		cancel()

		if err == nil {
			return cli, nil
		}
		cli.Close()
	}

	return nil, fmt.Errorf("could not connect to Docker daemon")
}

// IsAvailable returns whether Docker is available.
func (m *Manager) IsAvailable() bool {
	return m.available
}

// ensureNetwork creates the vega network if it doesn't exist.
func (m *Manager) ensureNetwork(ctx context.Context) error {
	if !m.available {
		return nil
	}

	networks, err := m.client.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("name", m.networkName)),
	})
	if err != nil {
		return err
	}

	if len(networks) > 0 {
		return nil
	}

	_, err = m.client.NetworkCreate(ctx, m.networkName, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			LabelManagedBy: "govega",
		},
	})
	return err
}

// ContainerConfig holds configuration for a project container.
type ContainerConfig struct {
	ProjectName string
	Image       string
	WorkDir     string
	Env         []string
	Ports       map[string]string // container port -> host port
}

// StartProject starts a container for a project.
func (m *Manager) StartProject(ctx context.Context, cfg ContainerConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available {
		return "", fmt.Errorf("docker not available")
	}

	containerName := containerPrefix + cfg.ProjectName

	// Check if container already exists
	existing, err := m.getContainer(ctx, containerName)
	if err == nil && existing != "" {
		// Container exists, check if running
		inspect, err := m.client.ContainerInspect(ctx, existing)
		if err == nil {
			if inspect.State.Running {
				return existing, nil
			}
			// Start existing stopped container
			if err := m.client.ContainerStart(ctx, existing, container.StartOptions{}); err != nil {
				return "", fmt.Errorf("failed to start existing container: %w", err)
			}
			return existing, nil
		}
	}

	// Use default image if not specified
	img := cfg.Image
	if img == "" {
		img = m.defaultImg
	}

	// Pull image if needed
	if err := m.ensureImage(ctx, img); err != nil {
		return "", fmt.Errorf("failed to pull image: %w", err)
	}

	// Create container config
	projectPath := filepath.Join(m.baseDir, "vega.work", "projects", cfg.ProjectName)
	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project path: %w", err)
	}
	if err := os.MkdirAll(absProjectPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory: %w", err)
	}

	containerCfg := &container.Config{
		Image:      img,
		WorkingDir: "/workspace",
		Env:        cfg.Env,
		Labels: map[string]string{
			LabelProject:   cfg.ProjectName,
			LabelManagedBy: "govega",
		},
		Tty:       true,
		OpenStdin: true,
		Cmd:       []string{"tail", "-f", "/dev/null"}, // Keep container running
		User:      "1000:1000",
	}

	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: absProjectPath,
				Target: "/workspace",
			},
		},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
		NetworkMode: "host",
	}

	var networkCfg *network.NetworkingConfig

	resp, err := m.client.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	if err := m.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	return resp.ID, nil
}

// StopProject stops a project's container.
func (m *Manager) StopProject(ctx context.Context, projectName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available {
		return fmt.Errorf("docker not available")
	}

	containerName := containerPrefix + projectName
	containerID, err := m.getContainer(ctx, containerName)
	if err != nil {
		return err
	}

	timeout := 10
	return m.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

// RemoveProject stops and removes a project's container.
func (m *Manager) RemoveProject(ctx context.Context, projectName string) error {
	if !m.available {
		return fmt.Errorf("docker not available")
	}

	containerName := containerPrefix + projectName

	m.mu.Lock()
	defer m.mu.Unlock()

	containerID, err := m.getContainer(ctx, containerName)
	if err != nil {
		return nil // Container doesn't exist, that's fine
	}

	// Stop if running
	timeout := 5
	_ = m.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})

	return m.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// ExecResult holds the result of a command execution.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Exec runs a command in a project's container.
// If the container doesn't exist, it will be auto-created with the default image.
func (m *Manager) Exec(ctx context.Context, projectName string, command []string, workDir string) (*ExecResult, error) {
	if !m.available {
		return nil, fmt.Errorf("docker not available")
	}

	containerName := containerPrefix + projectName

	m.mu.RLock()
	containerID, err := m.getContainer(ctx, containerName)
	m.mu.RUnlock()
	if err != nil {
		// Container doesn't exist - auto-create it with default config
		containerID, err = m.StartProject(ctx, ContainerConfig{
			ProjectName: projectName,
			Image:       m.defaultImg,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to auto-start container: %w", err)
		}
	}

	if workDir == "" {
		workDir = "/workspace"
	}

	execCfg := container.ExecOptions{
		Cmd:          command,
		WorkingDir:   workDir,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := m.client.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := m.client.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach exec: %w", err)
	}
	defer attachResp.Close()

	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read output: %w", err)
	}

	inspectResp, err := m.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect exec: %w", err)
	}

	return &ExecResult{
		ExitCode: inspectResp.ExitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// GetLogs returns logs from a project's container.
func (m *Manager) GetLogs(ctx context.Context, projectName string, tail int) (string, error) {
	if !m.available {
		return "", fmt.Errorf("docker not available")
	}

	containerName := containerPrefix + projectName

	m.mu.RLock()
	containerID, err := m.getContainer(ctx, containerName)
	m.mu.RUnlock()
	if err != nil {
		return "", fmt.Errorf("project container not found: %w", err)
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	}

	reader, err := m.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var output strings.Builder
	_, err = stdcopy.StdCopy(&output, &output, reader)
	if err != nil && err != io.EOF {
		return "", err
	}

	return output.String(), nil
}

// ProjectStatus holds the status of a project container.
type ProjectStatus struct {
	ContainerID string
	Running     bool
	Image       string
	Created     time.Time
}

// GetProjectStatus returns the status of a project's container.
func (m *Manager) GetProjectStatus(ctx context.Context, projectName string) (*ProjectStatus, error) {
	if !m.available {
		return &ProjectStatus{Running: false}, nil
	}

	containerName := containerPrefix + projectName

	m.mu.RLock()
	defer m.mu.RUnlock()

	containerID, err := m.getContainer(ctx, containerName)
	if err != nil {
		return &ProjectStatus{Running: false}, nil
	}

	inspect, err := m.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return &ProjectStatus{Running: false}, nil
	}

	created, _ := time.Parse(time.RFC3339Nano, inspect.Created)

	return &ProjectStatus{
		ContainerID: containerID[:12],
		Running:     inspect.State.Running,
		Image:       inspect.Config.Image,
		Created:     created,
	}, nil
}

// ListProjectContainers returns all vega-managed containers.
func (m *Manager) ListProjectContainers(ctx context.Context) ([]string, error) {
	if !m.available {
		return nil, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	containers, err := m.client.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelManagedBy+"=govega"),
		),
	})
	if err != nil {
		return nil, err
	}

	var names []string
	for _, c := range containers {
		if project, ok := c.Labels[LabelProject]; ok {
			names = append(names, project)
		}
	}
	return names, nil
}

// getContainer finds a container by name.
func (m *Manager) getContainer(ctx context.Context, name string) (string, error) {
	containers, err := m.client.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("name", name),
		),
	})
	if err != nil {
		return "", err
	}

	for _, c := range containers {
		for _, n := range c.Names {
			if n == "/"+name {
				return c.ID, nil
			}
		}
	}

	return "", fmt.Errorf("container not found: %s", name)
}

// ensureImage pulls an image if not present locally.
func (m *Manager) ensureImage(ctx context.Context, imageName string) error {
	_, _, err := m.client.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil // Image exists
	}

	reader, err := m.client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	// Consume the reader to complete the pull
	_, err = io.Copy(io.Discard, reader)
	return err
}

// Close closes the Docker client.
func (m *Manager) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
