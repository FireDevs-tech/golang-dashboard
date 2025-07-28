package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"minepanel/internal/docker"

	"github.com/gin-gonic/gin"
)

type PluginHandler struct {
	dockerClient *docker.Client
}

type Plugin struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	Tag            string     `json:"tag"`
	Contributors   string     `json:"contributors"`
	Likes          int        `json:"likes"`
	File           PluginFile `json:"file"`
	TestedVersions []string   `json:"testedVersions"`
	Rating         Rating     `json:"rating"`
	ReleaseDate    int64      `json:"releaseDate"`
	UpdateDate     int64      `json:"updateDate"`
	Downloads      int        `json:"downloads"`
	External       bool       `json:"external"`
	Icon           Icon       `json:"icon"`
	Premium        bool       `json:"premium"`
	Price          float64    `json:"price"`
	Currency       string     `json:"currency"`
	Author         Author     `json:"author"`
	Category       Category   `json:"category"`
	Version        Version    `json:"version"`
	Description    string     `json:"description"`
	Documentation  string     `json:"documentation"`
	SourceCodeLink string     `json:"sourceCodeLink"`
	DonationLink   string     `json:"donationLink"`
}

type PluginFile struct {
	Type        string  `json:"type"`
	Size        float64 `json:"size"`
	SizeUnit    string  `json:"sizeUnit"`
	URL         string  `json:"url"`
	ExternalURL string  `json:"externalUrl"`
}

type Rating struct {
	Count   int     `json:"count"`
	Average float64 `json:"average"`
}

type Icon struct {
	URL  string `json:"url"`
	Data string `json:"data"`
}

type Author struct {
	ID int `json:"id"`
}

type Category struct {
	ID int `json:"id"`
}

type Version struct {
	ID   int    `json:"id"`
	UUID string `json:"uuid"`
}

type PluginVersion struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	ReleaseDate int64  `json:"releaseDate"`
	Downloads   int    `json:"downloads"`
}

func NewPluginHandler(dockerClient *docker.Client) *PluginHandler {
	return &PluginHandler{
		dockerClient: dockerClient,
	}
}

// ListPlugins fetches available plugins from Spiget API
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	page := c.DefaultQuery("page", "1")
	size := c.DefaultQuery("size", "20")

	// Fetch plugins from Spiget API with retry logic
	url := fmt.Sprintf("https://api.spiget.org/v2/resources/free?size=%s&page=%s", size, page)

	var plugins []Plugin
	var lastError error
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Plugin list fetch attempt %d/%d", attempt, maxRetries)

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			lastError = fmt.Errorf("HTTP request failed: %v", err)
			log.Printf("Plugin list attempt %d failed: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastError = fmt.Errorf("API returned status: %d", resp.StatusCode)
			log.Printf("Plugin list attempt %d failed with status: %d", attempt, resp.StatusCode)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		err = json.NewDecoder(resp.Body).Decode(&plugins)
		resp.Body.Close()

		if err != nil {
			lastError = fmt.Errorf("failed to decode response: %v", err)
			log.Printf("Plugin list attempt %d failed to decode: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		// Success
		log.Printf("Successfully fetched %d plugins", len(plugins))
		c.JSON(http.StatusOK, gin.H{
			"plugins": plugins,
			"count":   len(plugins),
		})
		return
	}

	// All attempts failed
	log.Printf("Failed to fetch plugins after %d attempts: %v", maxRetries, lastError)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch plugins after multiple attempts"})
}

// GetPluginVersions fetches available versions for a specific plugin
func (h *PluginHandler) GetPluginVersions(c *gin.Context) {
	pluginID := c.Param("id")

	url := fmt.Sprintf("https://api.spiget.org/v2/resources/%s/versions", pluginID)

	var versions []PluginVersion
	var lastError error
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Plugin versions fetch attempt %d/%d for plugin %s", attempt, maxRetries, pluginID)

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			lastError = fmt.Errorf("HTTP request failed: %v", err)
			log.Printf("Plugin versions attempt %d failed: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastError = fmt.Errorf("API returned status: %d", resp.StatusCode)
			log.Printf("Plugin versions attempt %d failed with status: %d", attempt, resp.StatusCode)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		err = json.NewDecoder(resp.Body).Decode(&versions)
		resp.Body.Close()

		if err != nil {
			lastError = fmt.Errorf("failed to decode response: %v", err)
			log.Printf("Plugin versions attempt %d failed to decode: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		// Success
		log.Printf("Successfully fetched %d versions for plugin %s", len(versions), pluginID)
		c.JSON(http.StatusOK, gin.H{
			"versions": versions,
			"count":    len(versions),
		})
		return
	}

	// All attempts failed
	log.Printf("Failed to fetch plugin versions after %d attempts: %v", maxRetries, lastError)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch plugin versions after multiple attempts"})
}

// InstallPlugin downloads and installs a plugin to the server's plugins folder
func (h *PluginHandler) InstallPlugin(c *gin.Context) {
	serverID := c.Param("serverID")
	pluginID := c.Param("pluginID")
	versionID := c.DefaultQuery("version", "latest")

	// First check if server exists and is a Paper server
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Increased timeout for downloads
	defer cancel()

	// Get server type from Docker container environment
	serverType, err := h.dockerClient.GetServerType(ctx, serverID)
	if err != nil {
		log.Printf("Error getting server type: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check server type"})
		return
	}

	if serverType != "paper" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Plugins can only be installed on Paper servers"})
		return
	}

	// Get plugin info first to determine filename
	pluginInfo, err := h.getPluginInfoWithRetry(pluginID, 3)
	if err != nil {
		log.Printf("Error getting plugin info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get plugin info"})
		return
	}

	// Download plugin from Spiget API with retry logic
	var downloadURL string
	if versionID == "latest" {
		downloadURL = fmt.Sprintf("https://api.spiget.org/v2/resources/%s/download", pluginID)
	} else {
		downloadURL = fmt.Sprintf("https://api.spiget.org/v2/resources/%s/versions/%s/download", pluginID, versionID)
	}

	log.Printf("Downloading plugin %s from: %s", pluginInfo.Name, downloadURL)

	// Download the plugin with retry logic
	pluginData, err := h.downloadPluginWithRetry(downloadURL, 5)
	if err != nil {
		log.Printf("Error downloading plugin after retries: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to download plugin after multiple attempts"})
		return
	}

	// Generate filename for the plugin
	filename := fmt.Sprintf("%s.jar", pluginInfo.Name)
	// Clean filename to be filesystem safe
	filename = filepath.Base(filename)

	// Save plugin to server's plugins folder
	pluginPath := fmt.Sprintf("/data/plugins/%s", filename)

	if err := h.dockerClient.SaveFileToContainer(ctx, serverID, pluginPath, pluginData); err != nil {
		log.Printf("Error saving plugin to container: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to install plugin"})
		return
	}

	log.Printf("Successfully installed plugin %s (%d bytes) to server %s", pluginInfo.Name, len(pluginData), serverID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Plugin installed successfully",
		"plugin":   pluginInfo.Name,
		"filename": filename,
		"path":     pluginPath,
		"server":   serverID,
		"size":     len(pluginData),
	})
}

// Helper function to download plugin data with retry logic
func (h *PluginHandler) downloadPluginWithRetry(url string, maxRetries int) ([]byte, error) {
	var lastError error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Download attempt %d/%d for URL: %s", attempt, maxRetries, url)

		// Create HTTP client with timeout
		client := &http.Client{
			Timeout: 30 * time.Second,
		}

		resp, err := client.Get(url)
		if err != nil {
			lastError = fmt.Errorf("HTTP request failed: %v", err)
			log.Printf("Download attempt %d failed: %v", attempt, lastError)

			if attempt < maxRetries {
				waitTime := time.Duration(attempt*attempt) * time.Second // Exponential backoff
				log.Printf("Waiting %v before retry...", waitTime)
				time.Sleep(waitTime)
				continue
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastError = fmt.Errorf("HTTP status %d", resp.StatusCode)
			log.Printf("Download attempt %d failed with status: %d", attempt, resp.StatusCode)

			if attempt < maxRetries {
				waitTime := time.Duration(attempt*attempt) * time.Second
				log.Printf("Waiting %v before retry...", waitTime)
				time.Sleep(waitTime)
				continue
			}
			break
		}

		// Try to read the response body
		var data []byte
		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastError = fmt.Errorf("failed to read response body: %v", err)
			log.Printf("Download attempt %d failed to read body: %v", attempt, lastError)

			if attempt < maxRetries {
				waitTime := time.Duration(attempt*attempt) * time.Second
				log.Printf("Waiting %v before retry...", waitTime)
				time.Sleep(waitTime)
				continue
			}
			break
		}

		if len(data) == 0 {
			lastError = fmt.Errorf("received empty response body")
			log.Printf("Download attempt %d failed: empty response", attempt)

			if attempt < maxRetries {
				waitTime := time.Duration(attempt*attempt) * time.Second
				log.Printf("Waiting %v before retry...", waitTime)
				time.Sleep(waitTime)
				continue
			}
			break
		}

		log.Printf("Successfully downloaded plugin data: %d bytes", len(data))
		return data, nil
	}

	return nil, fmt.Errorf("download failed after %d attempts, last error: %v", maxRetries, lastError)
}

// Helper function to get plugin info with retry logic
func (h *PluginHandler) getPluginInfoWithRetry(pluginID string, maxRetries int) (*Plugin, error) {
	var lastError error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Plugin info fetch attempt %d/%d for plugin ID: %s", attempt, maxRetries, pluginID)

		plugin, err := h.getPluginInfo(pluginID)
		if err == nil {
			return plugin, nil
		}

		lastError = err
		log.Printf("Plugin info attempt %d failed: %v", attempt, err)

		if attempt < maxRetries {
			waitTime := time.Duration(attempt) * time.Second
			log.Printf("Waiting %v before retry...", waitTime)
			time.Sleep(waitTime)
		}
	}

	return nil, fmt.Errorf("failed to get plugin info after %d attempts, last error: %v", maxRetries, lastError)
}

// Helper function to get plugin info
func (h *PluginHandler) getPluginInfo(pluginID string) (*Plugin, error) {
	url := fmt.Sprintf("https://api.spiget.org/v2/resources/%s", pluginID)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status: %d", resp.StatusCode)
	}

	var plugin Plugin
	if err := json.NewDecoder(resp.Body).Decode(&plugin); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &plugin, nil
}

// SearchPlugins searches for plugins by name or tag
func (h *PluginHandler) SearchPlugins(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
		return
	}

	page := c.DefaultQuery("page", "1")
	size := c.DefaultQuery("size", "20")

	// Search plugins using Spiget API with retry logic
	url := fmt.Sprintf("https://api.spiget.org/v2/search/resources/%s?size=%s&page=%s", query, size, page)

	var plugins []Plugin
	var lastError error
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Plugin search attempt %d/%d for query: %s", attempt, maxRetries, query)

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			lastError = fmt.Errorf("HTTP request failed: %v", err)
			log.Printf("Plugin search attempt %d failed: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastError = fmt.Errorf("API returned status: %d", resp.StatusCode)
			log.Printf("Plugin search attempt %d failed with status: %d", attempt, resp.StatusCode)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		err = json.NewDecoder(resp.Body).Decode(&plugins)
		resp.Body.Close()

		if err != nil {
			lastError = fmt.Errorf("failed to decode response: %v", err)
			log.Printf("Plugin search attempt %d failed to decode: %v", attempt, lastError)

			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		// Success
		log.Printf("Successfully found %d plugins for query: %s", len(plugins), query)
		c.JSON(http.StatusOK, gin.H{
			"plugins": plugins,
			"count":   len(plugins),
			"query":   query,
		})
		return
	}

	// All attempts failed
	log.Printf("Failed to search plugins after %d attempts: %v", maxRetries, lastError)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search plugins after multiple attempts"})
}
