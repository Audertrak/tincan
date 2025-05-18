//go:build js && wasm

// tincan/cmd/tincan-wasm/main.go
package main

import (
	"fmt"
	"strings"
	"syscall/js" // For interacting with JavaScript
	//"time"

	"tincan/internal/client/core" // Your client core
)

var (
	clientCore *core.ClientCore
	// Global JS functions for callbacks to call
	jsDocument js.Value
	// We'll need references to specific DOM elements to update them
	// For simplicity, we'll get them as needed or store them if frequently used.
)

const (
	ServerIP   = "127.0.0.1" // Configure this appropriately, maybe from JS
	ServerPort = 8081
)

// --- Helper function to update DOM elements ---
func getElementById(id string) js.Value {
	return jsDocument.Call("getElementById", id)
}

func appendChatMessage(message string) {
	chatBox := getElementById("chatbox")
	if !chatBox.Truthy() {
		fmt.Println("Error: chatbox element not found")
		return
	}
	// Create a new div for the message
	// For raw text, we can just append. For styled messages, create elements.
	// For now, keep it simple. Server messages include newlines.
	// Browsers handle newlines in <textarea> or <pre>, but not directly in <div>.
	// Replace newlines with <br> for display in a div, or use a <pre> tag.
	// Let's assume chatBox is a <pre> or <textarea> for now.
	currentText := chatBox.Get("value").String() // Assuming textarea
	chatBox.Set("value", currentText+message)
	// Auto-scroll to bottom
	chatBox.Set("scrollTop", chatBox.Get("scrollHeight"))
}

func setStatusMessage(status string) {
	statusDiv := getElementById("status")
	if !statusDiv.Truthy() {
		fmt.Println("Error: status element not found")
		return
	}
	statusDiv.Set("textContent", status)
}

func showUsernamePrompt(show bool) {
	promptDiv := getElementById("usernamePrompt")
	connectButton := getElementById("connectButton")
	if !promptDiv.Truthy() || !connectButton.Truthy() {
		fmt.Println("Error: usernamePrompt or connectButton element not found")
		return
	}
	if show {
		promptDiv.Get("style").Set("display", "block")
		connectButton.Get("style").Set("display", "none") // Hide connect button
	} else {
		promptDiv.Get("style").Set("display", "none")
	}
}

func showChatInterface(show bool) {
	chatInterfaceDiv := getElementById("chatInterface")
	connectButton := getElementById("connectButton")
	if !chatInterfaceDiv.Truthy() || !connectButton.Truthy() {
		fmt.Println("Error: chatInterface or connectButton element not found")
		return
	}
	if show {
		chatInterfaceDiv.Get("style").Set("display", "block")
		connectButton.Get("style").Set("display", "none") // Hide connect button
		showUsernamePrompt(false)                         // Hide username prompt
	} else {
		chatInterfaceDiv.Get("style").Set("display", "none")
		connectButton.Get("style").Set("display", "block") // Show connect button
	}
}

// --- ClientCore Callbacks for WASM ---

func wasmOnStatusChange(statusMessage string) {
	fmt.Printf("WASM Status: %s\n", statusMessage) // Log to browser console
	setStatusMessage(statusMessage)
	if statusMessage == "Disconnected." || statusMessage == "Connection failed: Could not connect to server." {
		showChatInterface(false)
		showUsernamePrompt(false) // Ensure username prompt is also hidden
	}
}

func wasmOnMessageReceived(messageLine string) {
	fmt.Printf("WASM Message: %s", messageLine) // Log to browser console
	appendChatMessage(messageLine)
}

func wasmOnUsernameRequested() {
	fmt.Println("WASM: Server requests username.") // Log to browser console
	setStatusMessage("Server requests username. Please enter below.")
	showUsernamePrompt(true)
}

func wasmOnError(err error, context string) {
	errorMsg := fmt.Sprintf("WASM Core Error (%s): %v", context, err)
	fmt.Println(errorMsg)              // Log to browser console
	appendChatMessage(errorMsg + "\n") // Show error in chat too
	// Potentially update status or UI to reflect a critical error
}

func wasmOnLoginSuccess(username string) {
	fmt.Printf("WASM: Logged in as %s\n", username) // Log to browser console
	setStatusMessage(fmt.Sprintf("Logged in as %s.", username))
	showUsernamePrompt(false)
	showChatInterface(true)
	// Clear any old messages from chatbox before showing history/new messages
	chatBox := getElementById("chatbox")
	if chatBox.Truthy() {
		chatBox.Set("value", "") // Clear textarea
	}
}

// --- Functions exposed to JavaScript ---

// connectToServer is called by a button in HTML
func connectToServer(this js.Value, args []js.Value) interface{} {
	fmt.Println("WASM: connectToServer called")
	if clientCore == nil {
		clientCore = core.NewClientCore(
			wasmOnStatusChange,
			wasmOnMessageReceived,
			wasmOnUsernameRequested,
			wasmOnError,
			wasmOnLoginSuccess,
		)
	}
	if clientCore.IsConnected() {
		setStatusMessage("Already connected or connecting.")
		return nil
	}

	// In a real app, get IP/Port from UI or config
	go func() { // Run connect in a goroutine to avoid blocking JS main thread
		err := clientCore.Connect(ServerIP, ServerPort)
		if err != nil {
			// wasmOnStatusChange or wasmOnError would have been called by core
			fmt.Printf("WASM: Connection initiation error: %v\n", err)
		}
	}()
	return nil // JS functions can return values if needed
}

// submitUsername is called by a button in HTML
func submitUsername(this js.Value, args []js.Value) interface{} {
	usernameInput := getElementById("usernameInput")
	if !usernameInput.Truthy() {
		fmt.Println("Error: usernameInput element not found")
		return nil
	}
	username := usernameInput.Get("value").String()
	fmt.Printf("WASM: submitUsername called with: %s\n", username)

	if username == "" {
		setStatusMessage("Username cannot be empty.")
		return nil
	}
	if clientCore == nil || !clientCore.IsConnected() {
		setStatusMessage("Not connected to server.")
		return nil
	}

	err := clientCore.SendUsername(username)
	if err != nil {
		// wasmOnError would have been called by core
		fmt.Printf("WASM: Error sending username: %v\n", err)
	} else {
		setStatusMessage("Username sent. Waiting for server response...")
		// UI will update further based on server response via callbacks
	}
	return nil
}

// sendMessage is called by a button or Enter key in HTML
func sendMessage(this js.Value, args []js.Value) interface{} {
	messageInput := getElementById("messageInput")
	if !messageInput.Truthy() {
		fmt.Println("Error: messageInput element not found")
		return nil
	}
	message := messageInput.Get("value").String()
	fmt.Printf("WASM: sendMessage called with: %s\n", message)

	if message == "" {
		return nil // Don't send empty
	}
	if clientCore == nil || !clientCore.IsLoggedIn() {
		setStatusMessage("Not logged in. Cannot send message.")
		return nil
	}

	// Simple command parsing for /dm and /gm
	var err error
	if strings.HasPrefix(message, "/dm ") {
		parts := strings.SplitN(message, " ", 3)
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			appendChatMessage("System: Invalid DM format. Use: /dm <user> <message>\n")
			return nil
		}
		err = clientCore.SendDirectMessage(parts[1], parts[2])
	} else if strings.HasPrefix(message, "/gm ") {
		parts := strings.SplitN(message, " ", 3)
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			appendChatMessage("System: Invalid GM format. Use: /gm <group> <message>\n")
			return nil
		}
		err = clientCore.SendGroupMessage(parts[1], parts[2])
	} else if strings.HasPrefix(message, "/") {
		appendChatMessage(fmt.Sprintf("System: Unknown command: %s\n", message))
		return nil
	} else {
		err = clientCore.SendGlobalMessage(message)
	}

	if err != nil {
		// wasmOnError would have been called by core
		fmt.Printf("WASM: Error sending message: %v\n", err)
	} else {
		messageInput.Set("value", "") // Clear input field after sending
	}
	return nil
}

func main() {
	c := make(chan struct{}, 0) // Channel to keep Go program running

	fmt.Println("Tincan WASM Initialized (Go)")
	jsDocument = js.Global().Get("document")

	// Expose Go functions to JavaScript
	js.Global().Set("tincanConnect", js.FuncOf(connectToServer))
	js.Global().Set("tincanSubmitUsername", js.FuncOf(submitUsername))
	js.Global().Set("tincanSendMessage", js.FuncOf(sendMessage))

	// Initial UI state
	showChatInterface(false)
	showUsernamePrompt(false)
	setStatusMessage("Ready. Click 'Connect' to start.")

	<-c // Keep the Go program alive
}
