package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"go-software-factory/internal/controlplane/infra/web"
	"go-software-factory/internal/core/config"

	"github.com/joho/godotenv"
)

type Config struct {
	WebConfig       *web.WebConfig
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

var (
	errInvalidWebServerPort = errors.New("FACTORY_WEB_SERVER_PORT must be an integer from 1 to 65535")
	errInvalidAuthSecretKey = errors.New("WEB_AUTH_SECRET_KEY must contain at least 32 bytes")
)

func FromEnv() (*Config, error) {
	_ = godotenv.Load()
	port, err := strconv.Atoi(config.GetWithDefault("FACTORY_WEB_SERVER_PORT", "8716"))
	if err != nil || port < 1 || port > 65535 {
		return nil, errInvalidWebServerPort
	}
	secret := os.Getenv("WEB_AUTH_SECRET_KEY")
	if len(secret) < 32 {
		return nil, errInvalidAuthSecretKey
	}

	return &Config{
		WebConfig: &web.WebConfig{
			WebServerPort: port,
			AuthSecretKey: secret,
			Production:    os.Getenv("PRODUCTION") == "true",
		},
		StartupTimeout:  10 * time.Second,
		ShutdownTimeout: 15 * time.Second,
	}, nil
}
