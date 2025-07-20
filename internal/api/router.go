package api

import (
	"net/http"

	"minepanel/internal/docker"
	"minepanel/internal/handlers"

	"github.com/gin-gonic/gin"
)

func NewRouter(dockerClient *docker.Client) *gin.Engine {
	router := gin.New()

	// Middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "minepanel-api",
		})
	})

	// Initialize handlers
	minecraftHandler := handlers.NewMinecraftHandler(dockerClient)
	pluginHandler := handlers.NewPluginHandler(dockerClient)

	// API routes
	v1 := router.Group("/api/v1")
	{
		minecraft := v1.Group("/minecraft")
		{
			minecraft.POST("/create", minecraftHandler.CreateServer)
			minecraft.POST("/:id/start", minecraftHandler.StartServer)
			minecraft.POST("/:id/stop", minecraftHandler.StopServer)
			minecraft.GET("/:id/status", minecraftHandler.GetServerStatus)
			minecraft.GET("/servers", minecraftHandler.ListServers)
			minecraft.GET("/:id/logs", minecraftHandler.StreamServerLogs)             // WebSocket endpoint
			minecraft.GET("/:id/files", minecraftHandler.ListServerFiles)             // File browser endpoint
			minecraft.GET("/:id/files/download", minecraftHandler.DownloadServerFile) // File download endpoint
			minecraft.PUT("/:id/files/save", minecraftHandler.SaveServerFile)         // File save endpoint
		}

		plugins := v1.Group("/plugins")
		{
			plugins.GET("/", pluginHandler.ListPlugins)                               // List available plugins
			plugins.GET("/search", pluginHandler.SearchPlugins)                       // Search plugins
			plugins.GET("/:id/versions", pluginHandler.GetPluginVersions)             // Get plugin versions
			plugins.POST("/install/:serverID/:pluginID", pluginHandler.InstallPlugin) // Install plugin to server
		}
	}

	return router
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
