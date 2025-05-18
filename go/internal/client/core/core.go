// tincan/internal/client/core/core.go
package core

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

const (
	CoreBufferSize       = 1024
	CoreUsernameMaxLen   = 50 // Should match server's USERNAME_MAX_LEN
	CoreGroupNameMaxLen  = 50 // Should match server's GROUPNAME_MAX_LEN
	defaultServerTimeout = 0  // 0 for no timeout on read/write, can be adjusted
)

// Callback function types
type OnStatusChangeFunc func(statusMessage string)
type OnMessageReceivedFunc func(messageLine string) // Includes newlines from server
type OnUsernameRequestedFunc func()
type OnErrorFunc func(err error, context string) // For reporting errors to the UI/caller
type OnLoginSuccessFunc func(username string)

// ClientCore handles the client-side logic for Tincan chat.
type ClientCore struct {
	conn  net.Conn
	ws    js.Value
	isTCP bool

	// reader and writer for buffered I/O
	reader *bufio.Reader
	writer *bufio.Writer

	// Callbacks to notify the UI/consumer
	onStatusChange      OnStatusChangeFunc
	onMessageReceived   OnMessageReceivedFunc
	onUsernameRequested OnUsernameRequestedFunc
	onError             OnErrorFunc // For non-fatal errors or connection issues
	onLoginSuccess      OnLoginSuccessFunc

	username           string
	isConnected        bool
	loginPhaseComplete bool
	serverIP           string
	serverPort         int
	shutdownSignal     chan struct{}  // To signal the read goroutine to stop
	wg                 sync.WaitGroup // To wait for goroutines to finish
	mu                 sync.Mutex     // To protect access to connection state

}

// NewClientCore creates and initializes a new ClientCore instance.
func NewClientCore(
	onStatusChange OnStatusChangeFunc,
	onMessageReceived OnMessageReceivedFunc,
	onUsernameRequested OnUsernameRequestedFunc,
	onError OnErrorFunc,
	onLoginSuccess OnLoginSuccessFunc, // Added
) *ClientCore {
	// Provide default no-op callbacks if nil is passed
	nopStatus := func(string) {}
	nopMessage := func(string) {}
	nopUsername := func() {}
	nopError := func(error, string) {}
	nopLoginSuccess := func(string) {} // Added

	if onStatusChange == nil {
		onStatusChange = nopStatus
	}
	if onMessageReceived == nil {
		onMessageReceived = nopMessage
	}
	if onUsernameRequested == nil {
		onUsernameRequested = nopUsername
	}
	if onError == nil {
		onError = nopError
	}
	if onLoginSuccess == nil { // Added
		onLoginSuccess = nopLoginSuccess
	}

	return &ClientCore{
		onStatusChange:      onStatusChange,
		onMessageReceived:   onMessageReceived,
		onUsernameRequested: onUsernameRequested,
		onError:             onError,
		onLoginSuccess:      onLoginSuccess, // Added
		shutdownSignal:      make(chan struct{}),
	}
}

// Connect attempts to establish a connection with the Tincan server.
// This function will start a goroutine to handle incoming messages.
func (cc *ClientCore) Connect(ip string, port int) error {
	cc.mu.Lock()
	if cc.isConnected {
		cc.mu.Unlock()
		cc.onStatusChange("Already connected.")
		return nil
	}
	cc.mu.Unlock()

	// Call the platform-specific connection logic
	err := cc.platformConnect(ip, port) // platformConnect will set cc.conn or cc.ws
	if err != nil {
		// platformConnect should have already called onStatusChange/onError
		return err
	}

	cc.mu.Lock()
	cc.isConnected = true
	cc.loginPhaseComplete = false
	cc.serverIP = ip
	cc.serverPort = port
	cc.shutdownSignal = make(chan struct{})
	cc.mu.Unlock()

	// The onStatusChange for "Connected" should be called by platformConnect or here.
	// For WASM, onopen callback handles the "connected" state.
	// For TCP, platformConnect sets it up.
	if cc.isTCP {
		cc.onStatusChange(fmt.Sprintf("Connected to %s:%d (TCP).", ip, port))
	} // For WS, onopen callback will confirm.

	cc.wg.Add(1)
	if cc.isTCP {
		go cc.processIncomingMessagesNative()
	} else {
		go cc.processIncomingMessagesWasm()
	}
	return nil
}

// processIncomingMessages reads messages from the server and handles them.
// This method is intended to be run in a separate goroutine.
func (cc *ClientCore) processIncomingMessages() {
	defer cc.wg.Done()
	defer func() {
		// This defer ensures that if the loop exits (e.g. connection closed, error),
		// we attempt a clean disconnect.
		// Avoid calling Disconnect directly if it was initiated by Disconnect itself.
		cc.mu.Lock()
		wasConnected := cc.isConnected
		cc.mu.Unlock()
		if wasConnected { // Only if we thought we were connected
			cc.onStatusChange("Connection lost. Attempting to clean up.")
			cc.Disconnect() // This will handle cleanup
		}
	}()

	for {
		select {
		case <-cc.shutdownSignal:
			cc.onStatusChange("Shutdown signal received, stopping message processing.")
			return
		default:
			// Non-blocking check for connection status before read
			cc.mu.Lock()
			if !cc.isConnected || cc.conn == nil || cc.reader == nil {
				cc.mu.Unlock()
				// Connection might have been closed by Disconnect()
				return
			}
			// Optionally set a read deadline
			// cc.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			cc.mu.Unlock()

			line, err := cc.reader.ReadString('\n')

			// cc.mu.Lock()
			// cc.conn.SetReadDeadline(time.Time{}) // Clear deadline
			// cc.mu.Unlock()

			if err != nil {
				// Check if the error is due to the connection being closed by Disconnect()
				// or a genuine network error.
				cc.mu.Lock()
				shuttingDown := false
				select {
				case <-cc.shutdownSignal: // Check if shutdown was signaled
					shuttingDown = true
				default:
				}
				isConnected := cc.isConnected
				cc.mu.Unlock()

				if shuttingDown {
					// Normal shutdown, error is expected (e.g., "use of closed network connection")
					return
				}

				if isConnected { // Only report error if we thought we were connected
					if err == io.EOF {
						cc.onStatusChange("Server closed the connection.")
					} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
						// This case is for when SetReadDeadline is used and it times out.
						// For a continuous read loop, timeout might mean no data, not an error.
						// However, ReadString blocks, so a timeout here is unusual unless deadline set.
						// cc.onStatusChange("Read timeout.")
						// continue; // Or handle as appropriate
						cc.onError(err, "processIncomingMessages - ReadString timeout")
					} else {
						errMsg := fmt.Sprintf("Network error: %v", err)
						cc.onStatusChange(errMsg)
						cc.onError(err, "processIncomingMessages - ReadString")
					}
				}
				// Error or EOF, stop processing for this connection
				return // This will trigger the defer to call Disconnect
			}

			// Successfully read a line
			// The line includes the newline character.
			// Callbacks should expect this.
			cc.handleServerMessage(line)
		}
	}
}

// handleServerMessage processes a single message line from the server.
func (cc *ClientCore) handleServerMessage(rawLine string) {
	// rawLine includes the newline. For comparisons, trim it.
	trimmedLine := strings.TrimSpace(rawLine)

	cc.mu.Lock()
	inLoginPhase := !cc.loginPhaseComplete
	cc.mu.Unlock()

	if inLoginPhase {
		switch trimmedLine {
		case "REQ_USERNAME":
			cc.onUsernameRequested()
		case "SERVER_FULL":
			cc.onMessageReceived(rawLine) // Pass full message
			cc.onStatusChange("Server is full. Disconnecting.")
			go cc.Disconnect() // Disconnect in a goroutine to avoid deadlock if called from read loop
		default:
			if strings.HasPrefix(trimmedLine, "Welcome, ") {
				// Extract username from "Welcome, <username>!"
				// Example: "Welcome, alice!" -> "alice"
				var welcomeUsername string
				parts := strings.SplitN(trimmedLine, " ", 2) // ["Welcome,", "username!"]
				if len(parts) == 2 {
					namePart := parts[1]
					if strings.HasSuffix(namePart, "!") {
						welcomeUsername = namePart[:len(namePart)-1]
					} else {
						welcomeUsername = namePart // Should ideally have '!'
					}
				}

				cc.mu.Lock()
				cc.loginPhaseComplete = true
				cc.username = welcomeUsername // Store confirmed username
				cc.mu.Unlock()

				cc.onMessageReceived(rawLine) // Pass full welcome message
				if welcomeUsername != "" {
					cc.onLoginSuccess(welcomeUsername) // Invoke new callback
				} else {
					// Fallback or error if username couldn't be parsed, though server should ensure format
					cc.onLoginSuccess("user") // Or handle error
				}
			} else if strings.HasPrefix(trimmedLine, "BAD_USERNAME") ||
				strings.HasPrefix(trimmedLine, "NOT_ALLOWED") {
				cc.onMessageReceived(rawLine) // Pass full error message
				cc.onStatusChange("Login failed by server. Disconnecting.")
				go cc.Disconnect()
			} else {
				// Could be history or other pre-login messages
				cc.onMessageReceived(rawLine)
			}
		}
	} else { // Login phase complete, regular messages
		cc.onMessageReceived(rawLine)
	}
}

// sendToServer is a helper to send a formatted string to the server.
// It ensures a newline is appended.
func (cc *ClientCore) sendToServer(format string, args ...interface{}) error {
	cc.mu.Lock()
	if !cc.isConnected {
		cc.mu.Unlock()
		// ...
		return fmt.Errorf("not connected")
	}

	message := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(message, "\n") {
		message += "\n" // Ensure newline for server protocol consistency
	}

	var err error
	if cc.isTCP {
		if cc.writer == nil {
			cc.mu.Unlock()
			return fmt.Errorf("writer not initialized for TCP")
		}
		_, err = cc.writer.WriteString(message)
		if err == nil {
			err = cc.writer.Flush()
		}
	} else { // WebSocket
		if !cc.ws.Truthy() {
			cc.mu.Unlock()
			return fmt.Errorf("websocket not initialized")
		}
		cc.ws.Call("send", message) // WebSocket send method
		// WebSocket send doesn't typically return an error directly like this.
		// Errors are usually handled via 'onerror' or if the connection closes.
	}
	cc.mu.Unlock() // Unlock after send

	if err != nil {
		cc.onError(err, "sendToServer")
		// go cc.Disconnect() // Consider this
		return fmt.Errorf("failed to send to server: %w", err)
	}
	return nil
}

// SendUsername sends the chosen username to the server.
func (cc *ClientCore) SendUsername(username string) error {
	cc.mu.Lock()
	if !cc.isConnected {
		cc.mu.Unlock()
		cc.onStatusChange("Cannot send username: Not connected.")
		return fmt.Errorf("not connected")
	}
	if cc.loginPhaseComplete {
		cc.mu.Unlock()
		cc.onStatusChange("Cannot send username: Login already complete.")
		return fmt.Errorf("login already complete")
	}
	cc.mu.Unlock() // Unlock before sendToServer locks again

	if username == "" {
		cc.onStatusChange("Username cannot be empty.")
		// Server will likely send BAD_USERNAME, let server handle it.
		// Or return an error here: return fmt.Errorf("username cannot be empty")
	}
	if len(username) >= CoreUsernameMaxLen {
		cc.onStatusChange("Username too long.")
		// return fmt.Errorf("username too long")
	}

	return cc.sendToServer("%s", username)
}

// SendGlobalMessage sends a global chat message to the server.
func (cc *ClientCore) SendGlobalMessage(message string) error {
	cc.mu.Lock()
	if !cc.isConnected || !cc.loginPhaseComplete {
		cc.mu.Unlock()
		cc.onStatusChange("Cannot send message: Not connected or not logged in.")
		return fmt.Errorf("not connected or not logged in")
	}
	cc.mu.Unlock()

	if message == "" {
		return nil // Don't send empty messages
	}
	return cc.sendToServer("%s", message) // Server expects just the message
}

// SendDirectMessage sends a direct message to a recipient via the server.
func (cc *ClientCore) SendDirectMessage(recipient, message string) error {
	cc.mu.Lock()
	if !cc.isConnected || !cc.loginPhaseComplete {
		cc.mu.Unlock()
		cc.onStatusChange("Cannot send DM: Not connected or not logged in.")
		return fmt.Errorf("not connected or not logged in")
	}
	cc.mu.Unlock()

	if recipient == "" || message == "" {
		return fmt.Errorf("recipient and message cannot be empty for DM")
	}
	if len(recipient) >= CoreUsernameMaxLen {
		return fmt.Errorf("recipient username too long")
	}
	return cc.sendToServer("PRIVMSG %s %s", recipient, message)
}

// SendGroupMessage sends a message to a group via the server.
func (cc *ClientCore) SendGroupMessage(groupname, message string) error {
	cc.mu.Lock()
	if !cc.isConnected || !cc.loginPhaseComplete {
		cc.mu.Unlock()
		cc.onStatusChange("Cannot send group message: Not connected or not logged in.")
		return fmt.Errorf("not connected or not logged in")
	}
	cc.mu.Unlock()

	if groupname == "" || message == "" {
		return fmt.Errorf("groupname and message cannot be empty for group message")
	}
	if len(groupname) >= CoreGroupNameMaxLen {
		return fmt.Errorf("groupname too long")
	}
	return cc.sendToServer("GROUPMSG %s %s", groupname, message)
}

// Disconnect closes the connection to the server and cleans up resources.
func (cc *ClientCore) Disconnect() {
	cc.mu.Lock()
	if !cc.isConnected {
		cc.mu.Unlock()
		return
	}
	cc.onStatusChange("Disconnecting from server...")
	select {
	case <-cc.shutdownSignal: // Already closing or closed
	default:
		close(cc.shutdownSignal)
	}

	if cc.isTCP && cc.conn != nil {
		cc.conn.Close()
	} else if !cc.isTCP && cc.ws.Truthy() {
		// Check WebSocket state before closing: 0=CONNECTING, 1=OPEN, 2=CLOSING, 3=CLOSED
		readyState := cc.ws.Get("readyState").Int()
		if readyState == 0 || readyState == 1 { // CONNECTING or OPEN
			cc.ws.Call("close")
		}
	}
	cc.isConnected = false
	// ... (reset other fields)
	cc.mu.Unlock()
	cc.wg.Wait()
	cc.onStatusChange("Disconnected.")
}

// Cleanup releases any resources held by the ClientCore.
// For this implementation, it's largely the same as Disconnect.
// In Go, explicit cleanup is less common due to GC, but good for network resources.
func (cc *ClientCore) Cleanup() {
	cc.Disconnect() // Ensure connection is closed and goroutine stopped
	// Any other global cleanup for the core could go here if needed.
	cc.onStatusChange("Client core cleaned up.")
}

// IsConnected returns the current connection status.
func (cc *ClientCore) IsConnected() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.isConnected
}

// IsLoggedIn returns true if the username handshake is complete.
func (cc *ClientCore) IsLoggedIn() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.isConnected && cc.loginPhaseComplete
}
