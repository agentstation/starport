package controllers

// EventEmitter pushes one named event out through the configured webhook
// endpoints. The events dispatcher satisfies it, and a nil emitter pushes
// nothing, which is what a deployment with no webhook endpoint gets.
type EventEmitter interface {
	Emit(eventType string, data map[string]string)
}
