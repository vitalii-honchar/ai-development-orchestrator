package config

import "testing"

func TestFromEnvConfiguresWebServerPort(t *testing.T) {
	t.Setenv("WEB_AUTH_SECRET_KEY", "01234567890123456789012345678901")
	t.Setenv("FACTORY_WEB_SERVER_PORT", "8717")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebConfig.WebServerPort != 8717 || cfg.StartupTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		t.Fatalf("unexpected web configuration: %+v", cfg)
	}
}

func TestFromEnvRejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "65536", "invalid"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("FACTORY_WEB_SERVER_PORT", port)
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid port accepted")
			}
		})
	}
}

func TestSessionSecretAndProduction(t *testing.T) {
	t.Setenv("PRODUCTION", "true")
	for _, secret := range []string{"", "short", "01234567890123456789012345678901"} {
		t.Setenv("WEB_AUTH_SECRET_KEY", secret)
		cfg, err := FromEnv()
		if (err == nil) != (len(secret) >= 32) {
			t.Fatalf("secret validation: %v", err)
		}
		if err == nil && (!cfg.WebConfig.Production || cfg.WebConfig.AuthSecretKey != secret) {
			t.Fatal("web auth settings not configured")
		}
	}
}
