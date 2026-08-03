package main

import (
	"context"
	"fmt"
)

type Application struct {
	config config
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

func NewApplication() *Application {
	return &Application{
		config: defaultConfig(),
	}
}

func (app *Application) Run(_ctx context.Context) error {
	fmt.Println("Application is running...")

	return nil
}

func (app *Application) ParseConfigs(args []string) config {
	return config{}
}
