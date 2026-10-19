package server

type contextKey string

const (
	authenticatedUserIDContextKey  = contextKey("authenticatedUserID")
	authenticatedSessionContextKey = contextKey("authenticatedSession")
)
