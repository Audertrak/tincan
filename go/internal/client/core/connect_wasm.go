//go:build js && wasm

package core

import (
	"fmt"
	"syscall/js"
	"time"
	// "io" // For processIncomingMessagesWasm
)

// Connect attempts to establish a WebSocket connection.
func (cc *ClientCore) platformConnect(ip string, port int) error {
	// For WASM, ip and port construct the WebSocket URL.
	// The server's HTTP port (e.g., 8081) is used for the WS handshake.
	// The actual chat server port (8080) is not directly dialed by WASM.
	// We assume the WebSocket endpoint is on the same host/port as the HTTP server serving the client.
	// Or, it needs to be configurable.
	// Let's assume the WASM client knows its HTTP origin and appends /ws.
	// For local testing, if server is on localhost:8081, wsURL is ws://localhost:8081/ws

	// Get current window location to build wsURL relative to http server
	location := js.Global().Get("window").Get("location")
	protocol := "ws:"
	if location.Get("protocol").String() == "https:" {
		protocol = "wss:"
	}
	// Use the port from the HTTP server that serves the client files.
	// The `ip` and `port` params to Connect might be for the *chat* server,
	// but for WASM, we connect to the WS endpoint of the *web* server.
	// This needs clarification. For now, let's assume `ip` and `port` refer to the web server.
	wsURL := fmt.Sprintf("%s//%s:%d/ws", protocol, ip, port) // ip, port here are for the web server

	cc.onStatusChange(fmt.Sprintf("Connecting to %s (WebSocket)...", wsURL))

	ws, err := newWebSocket(wsURL) // newWebSocket is a helper we'll define
	if err != nil {
		errMsg := fmt.Sprintf("WebSocket Connection failed: %v", err)
		cc.onStatusChange(errMsg)
		cc.onError(err, "Connect - newWebSocket")
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	cc.mu.Lock()
	cc.ws = ws       // Add 'ws js.Value' field to ClientCore struct for WASM
	cc.isTCP = false // Add 'isTCP bool' field to ClientCore struct
	cc.mu.Unlock()
	return nil
}

// processIncomingMessagesWasm reads messages from the WebSocket.
func (cc *ClientCore) processIncomingMessagesWasm() {
	// This function will be called in a goroutine by Connect
	// It needs to set up JavaScript callbacks for ws.onmessage, ws.onclose, ws.onerror
	// For brevity, this is a simplified version. A robust one is more complex.
	// The actual message reading happens via the onmessage callback.
	// This goroutine mainly keeps things alive and handles cleanup signals.
	defer cc.wg.Done()

	onOpen := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		cc.onStatusChange("WebSocket connection opened.")
		// Connection is open, server should send REQ_USERNAME or similar.
		return nil
	})
	defer onOpen.Release()

	onMessage := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		event := args[0]
		messageData := event.Get("data").String()
		cc.handleServerMessage(messageData) // Assumes server sends lines with \n
		return nil
	})
	defer onMessage.Release()

	onError := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// event := args[0] // Error event
		cc.onError(fmt.Errorf("websocket error"), "WebSocket - onerror")
		cc.onStatusChange("WebSocket error. Disconnecting.")
		go cc.Disconnect() // Ensure disconnect is called
		return nil
	})
	defer onError.Release()

	onClose := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// event := args[0] // Close event
		cc.onStatusChange("WebSocket connection closed.")
		go cc.Disconnect() // Ensure disconnect is called
		return nil
	})
	defer onClose.Release()

	cc.mu.Lock()
	wsInstance := cc.ws
	cc.mu.Unlock()

	wsInstance.Set("onopen", onOpen)
	wsInstance.Set("onmessage", onMessage)
	wsInstance.Set("onerror", onError)
	wsInstance.Set("onclose", onClose)

	// Keep this goroutine alive until shutdownSignal or connection closes via callbacks
	<-cc.shutdownSignal
	// When shutdownSignal is closed, Disconnect should have already closed the WebSocket.
}

// Helper to create a new WebSocket connection using syscall/js
func newWebSocket(url string) (js.Value, error) {
	wsConstructor := js.Global().Get("WebSocket")
	if !wsConstructor.Truthy() {
		return js.Undefined(), fmt.Errorf("browser does not support WebSocket")
	}
	// This will attempt to connect immediately. Error/Open events handled by callbacks.
	return wsConstructor.Call("new", url), nil
}

// Add ws field to ClientCore struct in core.go (for wasm builds)
// And isTCP field
// type ClientCore struct {
//     // ... other fields
//     conn net.Conn // For native
//     ws js.Value   // For WASM
//     isTCP bool
//     reader *bufio.Reader
//     writer *bufio.Writer
// }
