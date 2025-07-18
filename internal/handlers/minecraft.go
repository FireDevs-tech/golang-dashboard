package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"minepanel/internal/docker"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

type MinecraftHandler struct {
	dockerClient *docker.Client
}

type CreateServerRequest struct {
	Name string `json:"name" binding:"required"`
	Port string `json:"port" binding:"required"`
}

type ServerResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Port    string `json:"port"`
	Message string `json:"message,omitempty"`
}

func NewMinecraftHandler(dockerClient *docker.Client) *MinecraftHandler {
	return &MinecraftHandler{
		dockerClient: dockerClient,
	}
}

func (h *MinecraftHandler) CreateServer(c *gin.Context) {
	var req CreateServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Invalid request data: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Received create server request - Name: %s, Port: %s", req.Name, req.Port)

	// Start container creation in background
	go func() {
		log.Printf("Starting background container creation for: %s", req.Name)
		ctx := context.Background() // No timeout for background process
		server, err := h.dockerClient.CreateMinecraftServer(ctx, req.Name, req.Port)
		if err != nil {
			log.Printf("Background container creation failed for %s: %v", req.Name, err)
		} else {
			log.Printf("Background container creation completed for %s: %s", req.Name, server.ID)
		}
	}()

	log.Printf("Returning immediate response for server: %s", req.Name)

	// Return immediate response
	c.JSON(http.StatusOK, ServerResponse{
		ID:      req.Name,
		Name:    req.Name,
		Status:  "creating",
		Port:    req.Port,
		Message: "Server creation started successfully",
	})
}

func (h *MinecraftHandler) StartServer(c *gin.Context) {
	serverID := c.Param("id")
	if serverID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Printf("Starting server: %s", serverID)

	// Start Docker container directly using serverID as container name/ID
	if err := h.dockerClient.StartMinecraftServer(ctx, serverID); err != nil {
		log.Printf("Failed to start server %s: %v", serverID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start server: " + err.Error()})
		return
	}

	log.Printf("Successfully started server: %s", serverID)

	c.JSON(http.StatusOK, ServerResponse{
		ID:      serverID,
		Name:    serverID,
		Status:  "running",
		Message: "Server started successfully. Connect to WebSocket at /api/v1/minecraft/" + serverID + "/logs for live logs",
	})
}

func (h *MinecraftHandler) StopServer(c *gin.Context) {
	serverID := c.Param("id")
	if serverID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop Docker container directly using serverID as container name/ID
	if err := h.dockerClient.StopMinecraftServer(ctx, serverID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stop server: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, ServerResponse{
		ID:      serverID,
		Name:    serverID,
		Status:  "stopped",
		Message: "Server stopped successfully",
	})
}

func (h *MinecraftHandler) GetServerStatus(c *gin.Context) {
	serverID := c.Param("id")
	if serverID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get current status from Docker directly using serverID as container name/ID
	status, err := h.dockerClient.GetMinecraftServerStatus(ctx, serverID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get server status: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, ServerResponse{
		ID:     serverID,
		Name:   serverID,
		Status: status,
	})
}

func (h *MinecraftHandler) ListServers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get servers from Docker
	dockerServers, err := h.dockerClient.ListMinecraftServers(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list servers: " + err.Error()})
		return
	}

	var servers []ServerResponse
	for _, server := range dockerServers {
		servers = append(servers, ServerResponse{
			ID:     server.ID,
			Name:   server.Name,
			Status: server.Status,
			Port:   server.Port,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"servers": servers,
		"count":   len(servers),
	})
}

// StreamServerLogs handles WebSocket connections for streaming server logs
func (h *MinecraftHandler) StreamServerLogs(c *gin.Context) {
	serverID := c.Param("id")
	if serverID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server ID is required"})
		return
	}

	log.Printf("DEBUG: WebSocket connection request for server logs: %s", serverID)

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("DEBUG: WebSocket connection established for server: %s", serverID)

	// Create context for managing the log streaming
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create channel for receiving logs
	logsChan := make(chan string, 100)

	// Start streaming logs in a goroutine
	go func() {
		defer close(logsChan)
		log.Printf("DEBUG: Starting log streaming goroutine for container: %s", serverID)
		if err := h.dockerClient.StreamContainerLogs(ctx, serverID, logsChan); err != nil {
			log.Printf("Error streaming logs for container %s: %v", serverID, err)
		}
		log.Printf("DEBUG: Log streaming goroutine ended for container: %s", serverID)
	}()

	// Handle WebSocket communication
	go func() {
		// Listen for client messages (ping/close)
		log.Printf("DEBUG: Starting WebSocket reader goroutine")
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				log.Printf("WebSocket read error for server %s: %v", serverID, err)
				cancel()
				return
			}
		}
	}()

	// Send logs to WebSocket client
	log.Printf("DEBUG: Starting main WebSocket message loop")
	for {
		select {
		case logLine, ok := <-logsChan:
			if !ok {
				log.Printf("DEBUG: Log channel closed for server: %s", serverID)
				return
			}

			// Ensure the log line is valid UTF-8 and clean
			cleanLogLine := strings.TrimSpace(logLine)
			if len(cleanLogLine) == 0 {
				continue
			}

			log.Printf("DEBUG: Received log line from channel: %q", cleanLogLine)
			// Send log line to WebSocket client
			if err := conn.WriteMessage(websocket.TextMessage, []byte(cleanLogLine)); err != nil {
				log.Printf("WebSocket write error for server %s: %v", serverID, err)
				return
			}
			log.Printf("DEBUG: Sent log line to WebSocket client")

		case <-ctx.Done():
			log.Printf("DEBUG: Context cancelled for server logs: %s", serverID)
			return

		case <-time.After(30 * time.Second):
			// Send ping to keep connection alive
			log.Printf("DEBUG: Sending ping to WebSocket client")
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("WebSocket ping error for server %s: %v", serverID, err)
				return
			}
		}
	}
}

// ListServerFiles handles file browsing requests
func (h *MinecraftHandler) ListServerFiles(c *gin.Context) {
	serverID := c.Param("id")
	path := c.Query("path")

	// If no path specified or root path, default to Minecraft server directory
	if path == "" || path == "/" {
		path = "/data"
	}

	// Ensure path starts with /data for security (only browse Minecraft files)
	if !strings.HasPrefix(path, "/data") {
		path = "/data" + path
	}

	log.Printf("File browse request for server %s, path: %s", serverID, path)

	// Get the list of files from the Docker container
	files, err := h.dockerClient.ListContainerFiles(c.Request.Context(), serverID, path)
	if err != nil {
		log.Printf("Error listing files in container %s: %v", serverID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"files": files,
		"path":  path,
	})
}

// DownloadServerFile handles file download requests
func (h *MinecraftHandler) DownloadServerFile(c *gin.Context) {
	serverID := c.Param("id")
	path := c.Query("path")

	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path parameter is required"})
		return
	}

	// Ensure path starts with /data for security (only download Minecraft files)
	if !strings.HasPrefix(path, "/data") {
		path = "/data" + path
	}

	log.Printf("File download request for server %s, path: %s", serverID, path)

	// Get the file content from the Docker container
	content, err := h.dockerClient.GetFileFromContainer(c.Request.Context(), serverID, path)
	if err != nil {
		log.Printf("Error getting file from container %s: %v", serverID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Determine content type based on file extension
	fileName := filepath.Base(path)
	var contentType string
	switch filepath.Ext(fileName) {
	case ".log":
		contentType = "text/plain"
	case ".json":
		contentType = "application/json"
	case ".properties":
		contentType = "text/plain"
	case ".jar":
		contentType = "application/java-archive"
	case ".dat":
		contentType = "application/octet-stream"
	default:
		contentType = "text/plain"
	}

	// Set headers for file download
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	c.Data(http.StatusOK, contentType, content)
}

// SaveServerFile handles file save requests
func (h *MinecraftHandler) SaveServerFile(c *gin.Context) {
	serverID := c.Param("id")
	path := c.Query("path")

	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path parameter is required"})
		return
	}

	// Ensure path starts with /data for security (only save Minecraft files)
	if !strings.HasPrefix(path, "/data") {
		path = "/data" + path
	}

	// Get the file content from request body
	var requestBody struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&requestBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	log.Printf("File save request for server %s, path: %s", serverID, path)

	// Save the file content to the Docker container
	err := h.dockerClient.SaveFileToContainer(c.Request.Context(), serverID, path, []byte(requestBody.Content))
	if err != nil {
		log.Printf("Error saving file to container %s: %v", serverID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "File saved successfully",
		"path":    path,
	})
}
