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

func NewApplication() *Application {
	return &Application{}
}

func (app *Application) Run(_ctx context.Context) error {
	fmt.Println("Application is running...")

	return nil
}

func (app *Application) ParseConfigs(args []string) config {
	return config{}
}
