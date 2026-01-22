package mobile

import (
	"encoding/json"
	"sync"
)

// EventCallback is the interface that native code (Kotlin/Swift) must implement
// to receive events from the GhostClient.
//
// gomobile will generate platform-specific interfaces:
// - Kotlin: interface EventCallback { fun onEvent(eventJSON: String) }
// - Swift: protocol EventCallback { func onEvent(_ eventJSON: String) }
type EventCallback interface {
	// OnEvent is called when an event occurs.
	// The eventJSON parameter is a JSON-encoded EventJSON struct.
	OnEvent(eventJSON string)
}

// eventDispatcher handles event callbacks in a thread-safe manner.
type eventDispatcher struct {
	mu       sync.RWMutex
	callback EventCallback
}

// newEventDispatcher creates a new event dispatcher.
func newEventDispatcher() *eventDispatcher {
	return &eventDispatcher{}
}

// setCallback sets the event callback.
func (d *eventDispatcher) setCallback(callback EventCallback) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callback = callback
}

// emit sends an event to the registered callback.
func (d *eventDispatcher) emit(event *EventJSON) {
	d.mu.RLock()
	callback := d.callback
	d.mu.RUnlock()

	if callback == nil {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	// Call callback in a goroutine to avoid blocking
	go callback.OnEvent(string(data))
}

// emitCandidate emits a candidate event.
func (d *eventDispatcher) emitCandidate(candidate *CandidateJSON) {
	candidateData, _ := json.Marshal(candidate)
	d.emit(&EventJSON{
		Type: EventTypeCandidate,
		Data: string(candidateData),
	})
}

// emitStateChange emits a state change event.
func (d *eventDispatcher) emitStateChange(state *ConnectionStateJSON) {
	stateData, _ := json.Marshal(state)
	d.emit(&EventJSON{
		Type: EventTypeStateChange,
		Data: string(stateData),
	})
}

// emitError emits an error event.
func (d *eventDispatcher) emitError(err error) {
	d.emit(&EventJSON{
		Type:    EventTypeError,
		Message: err.Error(),
	})
}

// emitConnected emits a connected event.
func (d *eventDispatcher) emitConnected(message string) {
	d.emit(&EventJSON{
		Type:    EventTypeConnected,
		Message: message,
	})
}

// emitDisconnected emits a disconnected event.
func (d *eventDispatcher) emitDisconnected(message string) {
	d.emit(&EventJSON{
		Type:    EventTypeDisconnected,
		Message: message,
	})
}

// emitTunnelUp emits a tunnel up event.
func (d *eventDispatcher) emitTunnelUp() {
	d.emit(&EventJSON{
		Type:    EventTypeTunnelUp,
		Message: "WireGuard tunnel is active",
	})
}

// emitTunnelDown emits a tunnel down event.
func (d *eventDispatcher) emitTunnelDown() {
	d.emit(&EventJSON{
		Type:    EventTypeTunnelDown,
		Message: "WireGuard tunnel is inactive",
	})
}
