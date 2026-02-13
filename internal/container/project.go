package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// StaleProjectAge is how long before a project is considered stale.
	StaleProjectAge = 30 * 24 * time.Hour // 30 days
)

// Project represents a managed project.
type Project struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Image       string            `json:"image"`
	Created     time.Time         `json:"created"`
	Status      string            `json:"status"` // created, running, stopped
	Env         map[string]string `json:"env,omitempty"`
}

// ProjectRegistry manages the project registry.
type ProjectRegistry struct {
	baseDir  string
	manager  *Manager
	mu       sync.RWMutex
	projects map[string]*Project
}

// NewProjectRegistry creates a new project registry.
func NewProjectRegistry(baseDir string, manager *Manager) (*ProjectRegistry, error) {
	r := &ProjectRegistry{
		baseDir:  baseDir,
		manager:  manager,
		projects: make(map[string]*Project),
	}

	// Ensure projects directory exists
	projectsDir := filepath.Join(baseDir, "vega.work", "projects")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		return nil, err
	}

	// Load existing registry
	if err := r.load(); err != nil {
		// Not fatal, just start fresh
	}

	return r, nil
}

// registryPath returns the path to projects.json.
func (r *ProjectRegistry) registryPath() string {
	return filepath.Join(r.baseDir, "vega.work", "projects.json")
}

// projectPath returns the path to a project's directory.
func (r *ProjectRegistry) projectPath(name string) string {
	return filepath.Join(r.baseDir, "vega.work", "projects", name)
}

// load reads the registry from disk.
func (r *ProjectRegistry) load() error {
	data, err := os.ReadFile(r.registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var projects []*Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, p := range projects {
		r.projects[p.Name] = p
	}

	return nil
}

// save writes the registry to disk.
func (r *ProjectRegistry) save() error {
	r.mu.RLock()
	projects := make([]*Project, 0, len(r.projects))
	for _, p := range r.projects {
		projects = append(projects, p)
	}
	r.mu.RUnlock()

	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(r.registryPath(), data, 0644)
}

// CreateProject creates a new project.
func (r *ProjectRegistry) CreateProject(ctx context.Context, name, description, image string) (*Project, error) {
	if image == "" {
		image = DefaultImage
	}

	// Lock and reserve the project name atomically
	r.mu.Lock()
	if _, exists := r.projects[name]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("project already exists: %s", name)
	}

	// Create project entry immediately to reserve the name
	project := &Project{
		Name:        name,
		Description: description,
		Image:       image,
		Created:     time.Now(),
		Status:      "creating",
	}
	r.projects[name] = project
	r.mu.Unlock()

	// Create project directory
	projectDir := r.projectPath(name)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		r.mu.Lock()
		delete(r.projects, name)
		r.mu.Unlock()
		return nil, fmt.Errorf("failed to create project directory: %w", err)
	}

	// Start container if Docker available
	status := "created"
	if r.manager.IsAvailable() {
		_, err := r.manager.StartProject(ctx, ContainerConfig{
			ProjectName: name,
			Image:       image,
		})
		if err != nil {
			r.mu.Lock()
			delete(r.projects, name)
			r.mu.Unlock()
			os.RemoveAll(projectDir)
			return nil, fmt.Errorf("failed to start container: %w", err)
		}
		status = "running"
	}

	// Update final status
	r.mu.Lock()
	project.Status = status
	r.mu.Unlock()

	if err := r.save(); err != nil {
		return nil, fmt.Errorf("failed to save registry: %w", err)
	}

	return project, nil
}

// GetProject returns a project by name.
func (r *ProjectRegistry) GetProject(name string) (*Project, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	project, exists := r.projects[name]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", name)
	}

	return project, nil
}

// GetOrCreateProject gets an existing project or creates a new one.
func (r *ProjectRegistry) GetOrCreateProject(ctx context.Context, name, description, image string) (*Project, error) {
	r.mu.RLock()
	project, exists := r.projects[name]
	r.mu.RUnlock()

	if exists {
		// Ensure container is running if Docker is available
		if r.manager != nil && r.manager.IsAvailable() {
			_, err := r.manager.StartProject(ctx, ContainerConfig{
				ProjectName: name,
				Image:       project.Image,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to start container for project %s: %v\n", name, err)
			} else {
				r.mu.Lock()
				project.Status = "running"
				r.mu.Unlock()
				r.save()
			}
		}
		return project, nil
	}

	// Try to create the project
	project, err := r.CreateProject(ctx, name, description, image)
	if err != nil {
		// If creation failed because project already exists (concurrent creation),
		// fetch and return the existing project
		r.mu.RLock()
		existingProject, exists := r.projects[name]
		r.mu.RUnlock()
		if exists {
			return existingProject, nil
		}
		return nil, err
	}
	return project, nil
}

// ListProjects returns all projects.
func (r *ProjectRegistry) ListProjects() []*Project {
	r.mu.RLock()
	defer r.mu.RUnlock()

	projects := make([]*Project, 0, len(r.projects))
	for _, p := range r.projects {
		projects = append(projects, p)
	}
	return projects
}

// DeleteProject removes a project.
func (r *ProjectRegistry) DeleteProject(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.projects[name]; !exists {
		return fmt.Errorf("project not found: %s", name)
	}

	// Stop and remove container
	if r.manager.IsAvailable() {
		_ = r.manager.RemoveProject(ctx, name)
	}

	// Remove from registry
	delete(r.projects, name)

	return r.save()
}

// StartProject starts a project's container.
func (r *ProjectRegistry) StartProject(ctx context.Context, name string) error {
	project, err := r.GetProject(name)
	if err != nil {
		return err
	}

	if !r.manager.IsAvailable() {
		return fmt.Errorf("docker not available")
	}

	_, err = r.manager.StartProject(ctx, ContainerConfig{
		ProjectName: name,
		Image:       project.Image,
	})
	if err != nil {
		return err
	}

	r.mu.Lock()
	project.Status = "running"
	r.mu.Unlock()

	return r.save()
}

// StopProject stops a project's container.
func (r *ProjectRegistry) StopProject(ctx context.Context, name string) error {
	project, err := r.GetProject(name)
	if err != nil {
		return err
	}

	if !r.manager.IsAvailable() {
		return fmt.Errorf("docker not available")
	}

	if err := r.manager.StopProject(ctx, name); err != nil {
		return err
	}

	r.mu.Lock()
	project.Status = "stopped"
	r.mu.Unlock()

	return r.save()
}

// Exec runs a command in a project's container.
func (r *ProjectRegistry) Exec(ctx context.Context, projectName string, command []string) (*ExecResult, error) {
	_, err := r.GetProject(projectName)
	if err != nil {
		return nil, err
	}

	if !r.manager.IsAvailable() {
		return nil, fmt.Errorf("docker not available - use direct mode")
	}

	return r.manager.Exec(ctx, projectName, command, "")
}

// GetProjectPath returns the host path to a project's directory.
func (r *ProjectRegistry) GetProjectPath(name string) string {
	return r.projectPath(name)
}

// Reconcile syncs the registry with actual container state.
func (r *ProjectRegistry) Reconcile(ctx context.Context) error {
	if !r.manager.IsAvailable() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for name, project := range r.projects {
		status, err := r.manager.GetProjectStatus(ctx, name)
		if err != nil {
			continue
		}

		if status.Running {
			project.Status = "running"
		} else {
			project.Status = "stopped"
		}
	}

	return r.save()
}

// ArchiveStaleProjects moves projects older than StaleProjectAge to an archive folder
// and removes them from the active registry.
func (r *ProjectRegistry) ArchiveStaleProjects(ctx context.Context) ([]string, error) {
	cutoff := time.Now().Add(-StaleProjectAge)
	var toArchive []string

	r.mu.RLock()
	for name, project := range r.projects {
		if project.Created.Before(cutoff) {
			projectDir := r.projectPath(name)
			info, err := os.Stat(projectDir)
			if err != nil || info.ModTime().Before(cutoff) {
				toArchive = append(toArchive, name)
			}
		}
	}
	r.mu.RUnlock()

	if len(toArchive) == 0 {
		return nil, nil
	}

	// Create archive directory
	archiveDir := filepath.Join(r.baseDir, "vega.work", "archive", "projects")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create archive directory: %w", err)
	}

	var archived []string
	for _, name := range toArchive {
		// Stop container if running
		if r.manager.IsAvailable() {
			_ = r.manager.StopProject(ctx, name)
			_ = r.manager.RemoveProject(ctx, name)
		}

		// Move project directory to archive
		srcDir := r.projectPath(name)
		dstDir := filepath.Join(archiveDir, fmt.Sprintf("%s-%s", name, time.Now().Format("2006-01-02")))

		if err := os.Rename(srcDir, dstDir); err != nil {
			fmt.Printf("[Cleanup] Warning: failed to archive project %s: %v\n", name, err)
			continue
		}

		// Remove from registry
		r.mu.Lock()
		delete(r.projects, name)
		r.mu.Unlock()

		archived = append(archived, name)
	}

	if len(archived) > 0 {
		if err := r.save(); err != nil {
			return archived, fmt.Errorf("failed to save registry: %w", err)
		}
	}

	return archived, nil
}
