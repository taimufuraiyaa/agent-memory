package plugin

import "errors"

var (
	// ErrPluginNotFound is returned when a plugin is not found.
	ErrPluginNotFound = errors.New("plugin not found")

	// ErrPluginAlreadyRegistered is returned when a plugin with the same name already exists.
	ErrPluginAlreadyRegistered = errors.New("plugin already registered")

	// ErrInvalidPluginType is returned when a plugin doesn't implement the expected interface.
	ErrInvalidPluginType = errors.New("invalid plugin type")

	// ErrPluginInitFailed is returned when plugin initialization fails.
	ErrPluginInitFailed = errors.New("plugin initialization failed")

	// ErrPluginShutdownFailed is returned when plugin shutdown fails.
	ErrPluginShutdownFailed = errors.New("plugin shutdown failed")
)
