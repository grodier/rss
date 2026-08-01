package main

import (
	"context"
	"fmt"
)

type Application struct{}

func NewApplication() *Application {
	return &Application{}
}

func (app *Application) Run(_ctx context.Context) error {
	fmt.Println("Application is running...")

	return nil
}
