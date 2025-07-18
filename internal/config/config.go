package config

import "os"

type Config struct {
	Environment string
	DockerHost  string
}

func New() *Config {
	return &Config{
		Environment: getEnv("ENVIRONMENT", "development"),
		DockerHost:  getEnv("DOCKER_HOST", "unix:///var/run/docker.sock"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
