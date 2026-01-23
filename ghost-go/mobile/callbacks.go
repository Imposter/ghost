package mobile

import (
	"encoding/json"
	"sync"
)

// EventCallback is the interface that native code (Kotlin/Swift) must implement
// to receive events from the GhostClient.
//
// # Thread Safety
//
// OnEvent may be called from any goroutine. Implementations MUST:
//   - Return quickly (do not block - dispatch to main thread internally if needed)
//   - Be safe for concurrent calls (events may fire from different goroutines)
//   - Not panic (panics will propagate and may crash the app)
//
// # Platform Bindings
//
// gomobile generates platform-specific interfaces:
//   - Kotlin: interface EventCallback { fun onEvent(eventJSON: String) }
//   - Swift: protocol EventCallback { func onEvent(_ eventJSON: String) }
//
// # Event Types
//
// The eventJSON parameter is a JSON-encoded EventJSON struct. Event types:
//   - "candidate": New ICE candidate discovered (during gathering)
//   - "state_change": ICE or tunnel state changed
//   - "connected": ICE connection established
//   - "disconnected": Peer disconnected
//   - "tunnel_up": WireGuard tunnel is active
//   - "tunnel_down": WireGuard tunnel is inactive
//   - "error": An error occurred
type EventCallback interface {
	// OnEvent is called when an event occurs.
	// The eventJSON parameter is a JSON-encoded EventJSON struct.
	// IMPORTANT: This method must return quickly and not block.
	OnEvent(eventJSON string)
}

// eventDispatcher handles event callbacks in a thread-safe manner.
//
// Thread-safe: All methods can be called from any goroutine.
// The callback is invoked synchronously - it MUST return quickly.
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
// The callback is invoked synchronously and must return quickly.
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

	// Call callback synchronously - it must return quickly
	callback.OnEvent(string(data))
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

// emitTunnelDownWithState emits a tunnel down event along with a state change event.
// This is useful when the tunnel goes down so the UI can update immediately.
func (d *eventDispatcher) emitTunnelDownWithState(state *ConnectionStateJSON) {
	d.emitTunnelDown()
	d.emitStateChange(state)
}
