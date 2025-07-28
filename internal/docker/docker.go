package docker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type Client struct {
	cli *client.Client
}

type MinecraftServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Image       string `json:"image"`
	Port        string `json:"port"`
	Type        string `json:"type"`
	ContainerID string `json:"container_id"`
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
	return c.cli.Close()
}

func (c *Client) CreateMinecraftServer(ctx context.Context, serverName, serverPort, serverType string) (*MinecraftServer, error) {
	imageName := "itzg/minecraft-server:latest"

	log.Printf("Starting container creation for server: %s on port: %s with type: %s", serverName, serverPort, serverType)

	_, err := c.cli.ImageInspect(ctx, imageName)
	if err != nil {
		log.Printf("Image %s not found locally, pulling...", imageName)
		reader, pullErr := c.cli.ImagePull(ctx, imageName, image.PullOptions{})
		if pullErr != nil {
			log.Printf("Failed to pull image %s: %v", imageName, pullErr)
			return nil, fmt.Errorf("failed to pull image: %w", pullErr)
		}
		defer reader.Close()

		log.Printf("Pulling image %s...", imageName)
		io.Copy(io.Discard, reader)
		log.Printf("Successfully pulled image %s", imageName)
	} else {
		log.Printf("Image %s already exists locally", imageName)
	}
	exposedPorts := nat.PortSet{
		"25565/tcp": struct{}{},
	}
	portBindings := nat.PortMap{
		"25565/tcp": []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: serverPort,
			},
		},
	}

	log.Printf("Creating container configuration for %s", serverName)

	env := []string{
		"EULA=TRUE",
		fmt.Sprintf("SERVER_NAME=%s", serverName),
		"VERSION=LATEST",
	}

	// Add server type specific environment variables
	switch strings.ToLower(serverType) {
	case "paper":
		env = append(env, "TYPE=PAPER")
	case "fabric":
		env = append(env, "TYPE=FABRIC")
	case "forge":
		env = append(env, "TYPE=FORGE")
	case "vanilla":
		env = append(env, "TYPE=VANILLA")
	default:
		env = append(env, "TYPE=VANILLA") // Default to vanilla
	}

	log.Printf("Environment variables for %s: %v", serverName, env)

	// Create container configuration
	config := &container.Config{
		Image:        imageName,
		Env:          env,
		ExposedPorts: exposedPorts,
	}

	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped",
		},
	}

	networkConfig := &network.NetworkingConfig{}
	platform := &v1.Platform{}

	log.Printf("Calling Docker API to create container: %s", serverName)
	resp, err := c.cli.ContainerCreate(ctx, config, hostConfig, networkConfig, platform, serverName)
	if err != nil {
		log.Printf("Failed to create container %s: %v", serverName, err)
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	log.Printf("Successfully created container %s with ID: %s", serverName, resp.ID[:12])

	return &MinecraftServer{
		ID:          resp.ID[:12], // Short ID
		Name:        serverName,
		Status:      "created",
		Image:       imageName,
		Port:        serverPort,
		ContainerID: resp.ID,
	}, nil
}

func (c *Client) StartMinecraftServer(ctx context.Context, containerID string) error {
	return c.cli.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (c *Client) StopMinecraftServer(ctx context.Context, containerID string) error {
	timeout := 30 // 30 seconds timeout
	return c.cli.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	})
}

func (c *Client) GetMinecraftServerStatus(ctx context.Context, containerID string) (string, error) {
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	return inspect.State.Status, nil
}

func (c *Client) ListMinecraftServers(ctx context.Context) ([]MinecraftServer, error) {
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}

	var servers []MinecraftServer
	for _, container := range containers {
		// Filter only minecraft server containers
		if container.Image == "itzg/minecraft-server:latest" ||
			len(container.Names) > 0 && container.Names[0] != "" {

			var port string
			if len(container.Ports) > 0 {
				port = fmt.Sprintf("%d", container.Ports[0].PublicPort)
			}

			// Get server type from container environment variables
			serverType := "vanilla" // default
			inspect, inspectErr := c.cli.ContainerInspect(ctx, container.ID)
			if inspectErr == nil {
				for _, env := range inspect.Config.Env {
					if strings.HasPrefix(env, "TYPE=") {
						serverType = strings.ToLower(strings.TrimPrefix(env, "TYPE="))
						break
					}
				}
			}

			servers = append(servers, MinecraftServer{
				ID:          container.ID[:12],
				Name:        container.Names[0][1:], // Remove leading /
				Status:      container.Status,
				Image:       container.Image,
				Port:        port,
				Type:        serverType,
				ContainerID: container.ID,
			})
		}
	}

	return servers, nil
}

// EnsureMinecraftImage pulls the Minecraft server image if it doesn't exist locally
// This can be called during application startup to pre-cache the image
func (c *Client) EnsureMinecraftImage(ctx context.Context) error {
	imageName := "itzg/minecraft-server:latest"

	// Check if image exists locally
	_, err := c.cli.ImageInspect(ctx, imageName)
	if err == nil {
		log.Printf("Image %s already exists locally", imageName)
		return nil
	}

	log.Printf("Pre-caching image %s in background...", imageName)
	go func() {
		reader, err := c.cli.ImagePull(context.Background(), imageName, image.PullOptions{})
		if err != nil {
			log.Printf("Pre-cache failed for image %s: %v", imageName, err)
			return
		}
		defer reader.Close()
		io.Copy(io.Discard, reader)
		log.Printf("Pre-cache completed for image %s", imageName)
	}()

	return nil
}

// StreamContainerLogs streams the logs from a container
func (c *Client) StreamContainerLogs(ctx context.Context, containerID string, logsChan chan<- string) error {
	log.Printf("DEBUG: StreamContainerLogs called for container: %s", containerID)

	// First check if container exists
	_, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Printf("DEBUG: Container inspection failed for %s: %v", containerID, err)
		return fmt.Errorf("container not found or not accessible: %w", err)
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
	}

	log.Printf("DEBUG: Getting container logs with options: %+v", options)
	reader, err := c.cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		log.Printf("Failed to get container logs for %s: %v", containerID, err)
		return err
	}
	defer reader.Close()

	log.Printf("Started streaming logs for container: %s", containerID)

	// Demultiplex Docker stream, send stdout and stderr to logsChan
	pr, pw := io.Pipe()
	// Use StdCopy to split the multiplexed stream
	go func() {
		defer pw.Close()
		if _, err := stdcopy.StdCopy(pw, pw, reader); err != nil {
			log.Printf("Error copying Docker logs for container %s: %v", containerID, err)
		}
	}()
	// Scan the demultiplexed log lines
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			log.Printf("Log streaming stopped for container: %s", containerID)
			return ctx.Err()
		default:
			logLine := scanner.Text()
			if len(logLine) == 0 {
				continue
			}
			log.Printf("DEBUG: Sending log line: %q", logLine)
			select {
			case logsChan <- logLine:
				log.Printf("DEBUG: Log line sent to channel")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error scanning logs for container %s: %v", containerID, err)
		return err
	}

	log.Printf("Log streaming ended for container: %s", containerID)
	return nil
}

// GetRecentContainerLogs gets recent logs from a container (non-streaming)
func (c *Client) GetRecentContainerLogs(ctx context.Context, containerID string, lines int) ([]string, error) {
	log.Printf("DEBUG: GetRecentContainerLogs called for container: %s, lines: %d", containerID, lines)

	// First check if container exists
	_, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Printf("DEBUG: Container inspection failed for %s: %v", containerID, err)
		return nil, fmt.Errorf("container not found or not accessible: %w", err)
	}

	// Convert lines to string for Docker API
	tailLines := "all"
	if lines > 0 {
		tailLines = strconv.Itoa(lines)
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false, // Don't follow, just get existing logs
		Timestamps: true,
		Tail:       tailLines,
	}

	log.Printf("DEBUG: Getting recent container logs with options: %+v", options)
	reader, err := c.cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		log.Printf("Failed to get recent container logs for %s: %v", containerID, err)
		return nil, err
	}
	defer reader.Close()

	// Demultiplex Docker stream and collect lines
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if _, err := stdcopy.StdCopy(pw, pw, reader); err != nil {
			log.Printf("Error copying Docker logs for container %s: %v", containerID, err)
		}
	}()

	var logLines []string
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		logLine := scanner.Text()
		if len(strings.TrimSpace(logLine)) > 0 {
			logLines = append(logLines, logLine)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error scanning recent logs for container %s: %v", containerID, err)
		return nil, err
	}

	log.Printf("DEBUG: Retrieved %d recent log lines for container: %s", len(logLines), containerID)
	return logLines, nil
}

// FileInfo represents a file or directory in the container
type FileInfo struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "directory"
	Size string `json:"size,omitempty"`
}

// ListContainerFiles lists files in a directory inside a container
func (c *Client) ListContainerFiles(ctx context.Context, containerID, path string) ([]FileInfo, error) {
	log.Printf("Listing files in container %s at path: %s", containerID, path)

	// Check if container is running
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	if !inspect.State.Running {
		return nil, fmt.Errorf("container %s is not running", containerID)
	}

	// If path is empty, default to root
	if path == "" {
		path = "/"
	}

	// Execute ls command in the container to get real file listing
	cmd := []string{"ls", "-la", path}
	log.Printf("Executing command in container: %v", cmd)

	// Create exec instance
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec instance: %w", err)
	}

	// Attach to exec instance
	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec instance: %w", err)
	}
	defer attachResp.Close()

	// Read the output
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check for errors
	if stderr.Len() > 0 {
		log.Printf("Error from ls command: %s", stderr.String())
		return nil, fmt.Errorf("ls command failed: %s", stderr.String())
	}

	// Parse the ls output
	files, err := c.parseLsOutput(stdout.String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse ls output: %w", err)
	}

	log.Printf("Found %d files in container %s at path %s", len(files), containerID, path)
	return files, nil
}

// parseLsOutput parses the output of `ls -la` command and returns FileInfo slice
func (c *Client) parseLsOutput(output string) ([]FileInfo, error) {
	var files []FileInfo
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip total line
		if strings.HasPrefix(line, "total ") {
			continue
		}

		// Parse ls -la output format: permissions links owner group size date time name
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue // Skip malformed lines
		}

		permissions := fields[0]
		sizeStr := fields[4]
		name := strings.Join(fields[8:], " ") // Handle filenames with spaces

		// Skip . and .. entries for cleaner display
		if name == "." || name == ".." {
			continue
		}

		// Determine file type
		fileType := "file"
		if strings.HasPrefix(permissions, "d") {
			fileType = "directory"
		}

		// Format size
		size := c.formatFileSize(sizeStr)

		files = append(files, FileInfo{
			Name: name,
			Type: fileType,
			Size: size,
		})
	}

	return files, nil
}

// formatFileSize formats file size for display
func (c *Client) formatFileSize(sizeStr string) string {
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return sizeStr
	}

	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	} else if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	} else {
		return fmt.Sprintf("%.1fGB", float64(size)/(1024*1024*1024))
	}
}

// GetFileFromContainer gets file content from a container as bytes
func (c *Client) GetFileFromContainer(ctx context.Context, containerID, containerPath string) ([]byte, error) {
	log.Printf("Getting file content from container %s: %s", containerID, containerPath)

	// Check if container is running
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	if !inspect.State.Running {
		return nil, fmt.Errorf("container %s is not running", containerID)
	}

	// Use cat command to read file content
	cmd := []string{"cat", containerPath}
	log.Printf("Executing command in container: %v", cmd)

	// Create exec instance
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec instance: %w", err)
	}

	// Attach to exec instance
	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec instance: %w", err)
	}
	defer attachResp.Close()

	// Read the output
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check for errors
	if stderr.Len() > 0 {
		log.Printf("Error from cat command: %s", stderr.String())
		return nil, fmt.Errorf("cat command failed: %s", stderr.String())
	}

	return stdout.Bytes(), nil
}

// SaveFileToContainer saves file content to a container
func (c *Client) SaveFileToContainer(ctx context.Context, containerID, containerPath string, content []byte) error {
	log.Printf("Saving file to container %s: %s", containerID, containerPath)

	// Check if container is running
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	if !inspect.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	// Use tee command to write file content
	// Create a temporary script that writes the content
	cmd := []string{"sh", "-c", fmt.Sprintf("cat > %s", containerPath)}
	log.Printf("Executing command in container: %v", cmd)

	// Create exec instance
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec instance: %w", err)
	}

	// Attach to exec instance
	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec instance: %w", err)
	}
	defer attachResp.Close()

	// Write the content to stdin
	go func() {
		defer attachResp.CloseWrite()
		attachResp.Conn.Write(content)
	}()

	// Read any output
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check for errors
	if stderr.Len() > 0 {
		log.Printf("Error from save command: %s", stderr.String())
		return fmt.Errorf("save command failed: %s", stderr.String())
	}

	log.Printf("Successfully saved file to container %s: %s", containerID, containerPath)
	return nil
}

// GetServerType returns the server type from the container environment variables
func (c *Client) GetServerType(ctx context.Context, containerID string) (string, error) {
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container: %w", err)
	}

	// Look for TYPE environment variable
	for _, env := range inspect.Config.Env {
		if strings.HasPrefix(env, "TYPE=") {
			serverType := strings.ToLower(strings.TrimPrefix(env, "TYPE="))
			return serverType, nil
		}
	}

	// Default to vanilla if no TYPE is set
	return "vanilla", nil
}
