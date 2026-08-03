package main

import (
	"context"
	"log/slog"
)

type Application struct {
	config config
	logger *slog.Logger
}

type config struct {
	env    string
	server serverConfig
}

type serverConfig struct {
	port int
}

func defaultConfig() config {
	return config{
		env: "development",
		server: serverConfig{
			port: 8080,
		},
	}
}

func NewApplication(logger *slog.Logger) *Application {
	return &Application{
		config: defaultConfig(),
		logger: logger,
	}
}

func (app *Application) Run(_ctx context.Context) error {
	app.logger.Info("Application is running...")

	return nil
}

func (app *Application) ParseConfigs(args []string) config {
	return config{}
}
