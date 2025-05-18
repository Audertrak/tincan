// tincan/internal/server/server.go
package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	PORT                  = ":8080"
	BUFFER_SIZE           = 1024 // This constant is not actively used in the Go version's logic directly
	USERNAME_MAX_LEN      = 50
	GROUPNAME_MAX_LEN     = 50
	CHAT_LOG_FILE         = "chat_log.txt"      // Relative to CWD
	ALLOWED_USERS_FILE    = "config/users.txt"  // Adjusted path
	GROUPS_FILE           = "config/groups.txt" // Adjusted path
	MAX_HISTORY_LINES     = 20
	MAX_ALLOWED_USERS     = 100 // Currently used for logging, not for hard limit in map
	MAX_GROUPS            = 20  // Currently used for logging, not for hard limit in map
	MAX_MEMBERS_PER_GROUP = 20  // Currently used for logging, not for hard limit in map
)

// ClientInfo holds information about a connected client
type ClientInfo struct {
	conn     net.Conn
	username string
	reader   *bufio.Reader
	writer   *bufio.Writer
	active   bool // True after successful username handshake
}

// GroupInfo holds information about a defined group
type GroupInfo struct {
	name    string
	members []string
}

var (
	clients            = make(map[net.Conn]*ClientInfo)
	allowedUsernames   = make(map[string]bool)
	groups             = make(map[string]*GroupInfo)
	clientsMutex       sync.RWMutex
	allowedUsersMutex  sync.RWMutex
	groupsMutex        sync.RWMutex
	chatHistory        []string
	chatHistoryMutex   sync.Mutex
	chatLogFileHandler *os.File
	webClientPath      = "clients/web"
	httpServerPort     = ":8081" // Port for the web client
)

func loadAllowedUsers() {
	allowedUsersMutex.Lock()
	defer allowedUsersMutex.Unlock()

	file, err := os.Open(ALLOWED_USERS_FILE)
	if err != nil {
		log.Printf(
			"Warning: Could not open %s: %v. No users will be allowed by default.",
			ALLOWED_USERS_FILE,
			err,
		)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	loadedCount := 0
	// Clear existing to support potential reload logic in future
	allowedUsernames = make(map[string]bool)
	for scanner.Scan() {
		username := strings.TrimSpace(scanner.Text())
		if username != "" {
			if len(username) >= USERNAME_MAX_LEN {
				log.Printf(
					"Warning: Username '%s' in %s exceeds max length and will be ignored.",
					username,
					ALLOWED_USERS_FILE,
				)
				continue
			}
			allowedUsernames[username] = true
			loadedCount++
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading %s: %v", ALLOWED_USERS_FILE, err)
	}
	log.Printf("Loaded %d allowed usernames from %s.", loadedCount, ALLOWED_USERS_FILE)
	if loadedCount > 0 {
		for u := range allowedUsernames {
			log.Printf("  - %s", u)
		}
	}
}

func isUsernameAllowed(username string) bool {
	allowedUsersMutex.RLock()
	defer allowedUsersMutex.RUnlock()
	_, ok := allowedUsernames[username]
	return ok
}

func loadGroups() {
	groupsMutex.Lock()
	defer groupsMutex.Unlock()

	file, err := os.Open(GROUPS_FILE)
	if err != nil {
		log.Printf(
			"Warning: Could not open %s: %v. No groups will be available.",
			GROUPS_FILE,
			err,
		)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	loadedCount := 0
	// Clear existing to support potential reload logic in future
	groups = make(map[string]*GroupInfo)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			log.Printf("Skipping malformed group line: %s", line)
			continue
		}
		groupName := strings.TrimSpace(parts[0])
		memberStr := strings.TrimSpace(parts[1])

		if len(groupName) >= GROUPNAME_MAX_LEN {
			log.Printf(
				"Warning: Group name '%s' in %s exceeds max length and will be ignored.",
				groupName,
				GROUPS_FILE,
			)
			continue
		}
		if groupName == "" || memberStr == "" {
			log.Printf("Skipping group with empty name or members: %s", line)
			continue
		}

		group := &GroupInfo{name: groupName}
		members := strings.Split(memberStr, ",")
		for _, memberName := range members {
			m := strings.TrimSpace(memberName)
			if m != "" {
				if len(m) >= USERNAME_MAX_LEN {
					log.Printf(
						"Warning: Member name '%s' in group '%s' (%s) exceeds max length and will be ignored.",
						m,
						groupName,
						GROUPS_FILE,
					)
					continue
				}
				// Optional: Check if member is an allowed user
				// if !isUsernameAllowed(m) {
				//    log.Printf("Warning: Member '%s' in group '%s' (%s) is not an allowed user and will be ignored.", m, groupName, GROUPS_FILE)
				//    continue
				// }
				group.members = append(group.members, m)
			}
		}
		if len(group.members) > 0 {
			groups[groupName] = group
			loadedCount++
		} else {
			log.Printf(
				"Group '%s' loaded with no valid members and will be ignored.",
				groupName,
			)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading %s: %v", GROUPS_FILE, err)
	}
	log.Printf("Loaded %d groups from %s.", loadedCount, GROUPS_FILE)
	if loadedCount > 0 {
		for _, g := range groups {
			log.Printf("  - Group '%s': %d members", g.name, len(g.members))
		}
	}
}

func getTimestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func logChatMessage(message string) {
	// Assumes chatLogFileHandler is initialized
	if chatLogFileHandler == nil {
		log.Printf(
			"Error: chatLogFileHandler is nil. Cannot write: %s",
			message,
		)
		return
	}
	// Ensure message has a newline for consistent log format
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	logEntry := fmt.Sprintf("[%s] %s", getTimestamp(), message)
	if _, err := chatLogFileHandler.WriteString(logEntry); err != nil {
		log.Printf("Error writing to chat log: %v", err)
	}
}

func addMessageToHistory(message string) {
	chatHistoryMutex.Lock()
	defer chatHistoryMutex.Unlock()
	// Ensure message has a newline for consistent history format
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	chatHistory = append(chatHistory, message)
	if len(chatHistory) > MAX_HISTORY_LINES {
		chatHistory = chatHistory[len(chatHistory)-MAX_HISTORY_LINES:]
	}
}

func sendToClient(client *ClientInfo, message string) {
	if client == nil || client.writer == nil {
		log.Println("Attempted to send to nil client or client with nil writer.")
		return
	}
	// Ensure message has a newline for client protocol
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	_, err := client.writer.WriteString(message)
	if err != nil {
		log.Printf(
			"Error sending message to %s (%s): %v",
			client.username,
			client.conn.RemoteAddr().String(),
			err,
		)
		// Consider closing connection or marking client for removal
		return
	}
	err = client.writer.Flush()
	if err != nil {
		log.Printf(
			"Error flushing writer for %s (%s): %v",
			client.username,
			client.conn.RemoteAddr().String(),
			err,
		)
	}
}

func broadcastMessage(message string, excludeConn net.Conn) {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	// Ensure message has a newline
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	for conn, client := range clients {
		if client.active && conn != excludeConn {
			sendToClient(client, message)
		}
	}
}

func handleConnection(conn net.Conn) {
	log.Printf("New connection attempt from: %s", conn.RemoteAddr().String())
	client := &ClientInfo{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		active: false,
	}

	defer func() {
		conn.Close()
		clientsMutex.Lock()
		// Check if the client was ever added (it should be)
		if c, ok := clients[conn]; ok {
			// Use the username from the map, as client.username might not be set if login failed early
			usernameForLog := c.username
			if usernameForLog == "" {
				usernameForLog = "[unauthenticated]"
			}
			isActive := c.active

			delete(clients, conn)
			clientsMutex.Unlock() // Unlock before logging and broadcasting

			if isActive {
				log.Printf(
					"User %s (%s) disconnected.",
					usernameForLog,
					conn.RemoteAddr().String(),
				)
				systemMsg := fmt.Sprintf("System: %s has left the chat.", usernameForLog)
				logChatMessage(systemMsg)
				addMessageToHistory(systemMsg)
				broadcastMessage(systemMsg, nil) // Broadcast to all remaining
			} else {
				log.Printf(
					"Connection from %s (user: %s) closed before completing login or was rejected.",
					conn.RemoteAddr().String(),
					usernameForLog,
				)
			}
		} else {
			clientsMutex.Unlock() // Ensure unlock if client wasn't found (should not happen)
			log.Printf(
				"Connection from %s closed, but client was not found in active map.",
				conn.RemoteAddr().String(),
			)
		}
	}()

	clientsMutex.Lock()
	clients[conn] = client
	clientsMutex.Unlock()

	sendToClient(client, "REQ_USERNAME")
	usernameLine, err := client.reader.ReadString('\n')
	if err != nil {
		if err != io.EOF { // Don't be too verbose for normal disconnects
			log.Printf(
				"Error reading username from %s: %v",
				conn.RemoteAddr().String(),
				err,
			)
		}
		return // This will trigger defer, cleaning up the client
	}
	username := strings.TrimSpace(usernameLine)

	if username == "" {
		sendToClient(client, "BAD_USERNAME\nUsername cannot be empty.")
		log.Printf("Client %s sent empty username.", conn.RemoteAddr().String())
		return
	}
	if len(username) >= USERNAME_MAX_LEN {
		sendToClient(client, "BAD_USERNAME\nUsername too long.")
		log.Printf(
			"Client %s sent username too long: %s",
			conn.RemoteAddr().String(),
			username,
		)
		return
	}
	if !isUsernameAllowed(username) {
		sendToClient(client, "NOT_ALLOWED\nUsername not on allowed list.")
		log.Printf(
			"Username '%s' from %s is not allowed. Rejecting.",
			username,
			conn.RemoteAddr().String(),
		)
		return
	}

	// Check if username is already in use by another active client
	clientsMutex.RLock()
	alreadyExists := false
	for _, existingClient := range clients {
		// Check existingClient.conn != conn to allow a user to reconnect if their old session is still being cleaned up
		// but primarily, check active status and username.
		if existingClient.active && existingClient.username == username && existingClient.conn != conn {
			alreadyExists = true
			break
		}
	}
	clientsMutex.RUnlock()

	if alreadyExists {
		sendToClient(client, "BAD_USERNAME\nUsername already in use.")
		log.Printf(
			"Client %s (%s) tried to use username '%s' which is already active.",
			conn.RemoteAddr().String(),
			username,
			username,
		)
		return
	}
	// Update client info under lock
	clientsMutex.Lock()
	client.username = username // Set username in the map's copy too
	client.active = true
	clients[conn] = client // Re-assign to update the map's value if ClientInfo is a value type (it is)
	clientsMutex.Unlock()

	log.Printf(
		"Username '%s' (allowed) received for %s.",
		username,
		conn.RemoteAddr().String(),
	)
	sendToClient(client, fmt.Sprintf("Welcome, %s!", username))

	chatHistoryMutex.Lock()
	if len(chatHistory) > 0 {
		sendToClient(client, "--- Recent Chat History ---")
		for _, histMsg := range chatHistory {
			sendToClient(client, histMsg) // histMsg already has newline
		}
		sendToClient(client, "--- End of History ---")
	}
	chatHistoryMutex.Unlock()

	joinMsg := fmt.Sprintf("System: %s has joined the chat.", username)
	logChatMessage(joinMsg)
	addMessageToHistory(joinMsg)
	broadcastMessage(joinMsg, conn)

	for {
		message, err := client.reader.ReadString('\n')
		if err != nil {
			// Normal EOF or connection closed by peer is not an "error" to spam logs with
			// It will be handled by the defer block.
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") && !strings.Contains(err.Error(), "connection reset by peer") {
				log.Printf(
					"Error reading from %s (%s): %v",
					client.username,
					conn.RemoteAddr().String(),
					err,
				)
			}
			break
		}

		fullMessageCmd := strings.TrimSpace(message)
		// rawMessageWithNewline := message // Retain original for broadcasting if needed

		log.Printf("Received from %s: %s", client.username, strings.TrimSpace(message))

		if strings.HasPrefix(fullMessageCmd, "PRIVMSG ") {
			parts := strings.SplitN(fullMessageCmd, " ", 3)
			if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
				sendToClient(client, "System: Invalid DM format. Use: PRIVMSG <user> <message>")
				continue
			}
			recipientUsername := parts[1]
			dmText := parts[2]

			var recipientClient *ClientInfo
			foundRecipient := false
			clientsMutex.RLock()
			for _, rc := range clients {
				if rc.active && rc.username == recipientUsername {
					recipientClient = rc
					foundRecipient = true
					break
				}
			}
			clientsMutex.RUnlock()

			if foundRecipient && recipientClient != nil {
				dmToRecipient := fmt.Sprintf("(DM from %s): %s", client.username, dmText)
				sendToClient(recipientClient, dmToRecipient)
				dmToSender := fmt.Sprintf("(DM to %s): %s", recipientUsername, dmText)
				sendToClient(client, dmToSender)

				dmLog := fmt.Sprintf(
					"DM from %s to %s: %s",
					client.username,
					recipientUsername,
					dmText,
				)
				logChatMessage(dmLog)
				addMessageToHistory(dmLog)
			} else {
				sendToClient(
					client,
					fmt.Sprintf("System: User '%s' not found or is offline.", recipientUsername),
				)
			}
		} else if strings.HasPrefix(fullMessageCmd, "GROUPMSG ") {
			parts := strings.SplitN(fullMessageCmd, " ", 3)
			if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
				sendToClient(client, "System: Invalid GM format. Use: GROUPMSG <group> <message>")
				continue
			}
			groupNameReq := parts[1]
			gmText := parts[2]

			groupsMutex.RLock()
			group, ok := groups[groupNameReq]
			groupsMutex.RUnlock()

			if ok {
				membersMessaged := 0
				gmToSend := fmt.Sprintf(
					"(#%s from %s): %s",
					group.name,
					client.username,
					gmText,
				)
				clientsMutex.RLock()
				for _, memberUsername := range group.members {
					for _, c := range clients {
						if c.active && c.username == memberUsername {
							// Don't send to self if sender is part of group, unless desired
							// if c.conn != client.conn {
							sendToClient(c, gmToSend)
							membersMessaged++
							// }
							break // Found this member, move to next member in group list
						}
					}
				}
				clientsMutex.RUnlock()

				confirmationToSender := fmt.Sprintf("(To #%s): %s", group.name, gmText)
				sendToClient(client, confirmationToSender)

				gmLog := fmt.Sprintf(
					"GROUPMSG to #%s from %s: %s",
					group.name,
					client.username,
					gmText,
				)
				logChatMessage(gmLog)
				addMessageToHistory(gmLog)
				log.Printf("%s (%d members messaged)", gmLog, membersMessaged)
			} else {
				sendToClient(client, fmt.Sprintf("System: Group '#%s' not found.", groupNameReq))
			}
		} else {
			// Global message, ensure original message (with newline) is used for formatting
			globalMsg := fmt.Sprintf("%s: %s", client.username, message) // message already has \n
			logChatMessage(strings.TrimSuffix(globalMsg, "\n"))          // Log without double newline
			addMessageToHistory(strings.TrimSuffix(globalMsg, "\n"))
			broadcastMessage(globalMsg, nil) // Send to all, including sender
		}
	}
}

// serveWs handles websocket requests from the peer.
func serveWs(w http.ResponseWriter, r *http.Request) {
	log.Printf("WebSocket: Incoming connection attempt from %s", r.RemoteAddr)
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Subprotocols:       []string{"tincan-chat"}, // Optional: if you define subprotocols
		InsecureSkipVerify: false, // Set to true if using self-signed certs for WSS locally (not recommended for prod)
		OriginPatterns:     nil,   // nil allows all origins, or specify patterns like ["localhost:*", "yourdomain.com"]
	})
	if err != nil {
		log.Printf("WebSocket: Accept error from %s: %v", r.RemoteAddr, err)
		// The library handles sending the HTTP error response.
		return
	}
	// Use r.Context() for the websocket connection's context.
	// It will be cancelled when the underlying HTTP connection is closed.
	handleWebSocketConnection(r.Context(), wsConn, r.RemoteAddr)
}

// handleWebSocketConnection manages a single WebSocket client connection.
// It mirrors the logic of handleConnection but for WebSockets.
func handleWebSocketConnection(ctx context.Context, wsConn *websocket.Conn, remoteAddr string) {
	log.Printf("WebSocket: Connection established with %s", remoteAddr)

	// Create a wrapper for the websocket connection to somewhat mimic net.Conn for ClientInfo
	// This is a simplification. A more robust solution might involve an interface.
	client := &ClientInfo{
		// conn:   wsConn, // wsConn is not a net.Conn. We'll handle reads/writes differently.
		// reader: bufio.NewReader(wsConn), // Not directly applicable
		// writer: bufio.NewWriter(wsConn), // Not directly applicable
		active: false,
		// We need a way to associate this wsConn with the client in the 'clients' map.
		// For now, let's manage it slightly differently or adapt ClientInfo.
		// Let's try to keep ClientInfo similar and handle I/O specially.
		// A unique ID for the wsConn might be needed if we put it in the global clients map.
		// For now, this function will be self-contained for the client's lifecycle.
	}
	// For broadcasting, we'd need to register this client.
	// Let's use a temporary structure for this client for now.
	// This part needs careful thought on how to integrate with the existing client management.

	// Simplified client management for this example:
	// We'll need to adapt the global 'clients' map or have a separate one for WebSockets,
	// or make ClientInfo more generic.
	// For now, let's focus on the single connection lifecycle.

	// Defer cleanup for this specific WebSocket connection
	defer func() {
		wsConn.Close(websocket.StatusNormalClosure, "Connection closed by server")
		log.Printf("WebSocket: Connection with %s (user: %s) closed.", remoteAddr, client.username)
		// If this client was registered in a global map, remove it here.
		// And broadcast departure message.
		if client.active {
			// This requires ClientInfo to be in the global map and accessible.
			// This part needs to be integrated with the global clients map and mutex.
			// For now, conceptual:
			clientsMutex.Lock()
			// Find and delete client by wsConn or a unique ID if we adapt the clients map.
			// For simplicity, let's assume we'd have a way to remove it.
			// delete(clients, client.conn) // This 'conn' would need to be the key
			clientsMutex.Unlock()

			systemMsg := fmt.Sprintf("System: %s has left the chat.\n", client.username)
			logChatMessage(systemMsg)
			addMessageToHistory(systemMsg)
			// broadcastMessage(systemMsg, client.conn) // 'conn' needs to be the right type or ID
			// Broadcasting to WebSockets also needs adaptation.
			broadcastWebSocketMessage(systemMsg, wsConn) // A new broadcast function
		}
	}()

	// Helper to send a message to this specific WebSocket client
	sendToWsClient := func(msg string) error {
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n" // Ensure newline for consistency if clients expect it
		}
		err := wsConn.Write(ctx, websocket.MessageText, []byte(msg))
		if err != nil {
			log.Printf("WebSocket: Error writing to %s (user: %s): %v", remoteAddr, client.username, err)
		}
		return err
	}

	// Username Handshake
	if err := sendToWsClient("REQ_USERNAME"); err != nil {
		return
	}

	msgType, usernameBytes, err := wsConn.Read(ctx)
	if err != nil {
		log.Printf("WebSocket: Error reading username from %s: %v", remoteAddr, err)
		return
	}
	if msgType != websocket.MessageText {
		log.Printf("WebSocket: Received non-text message for username from %s", remoteAddr)
		wsConn.Close(websocket.StatusUnsupportedData, "Expected text message for username")
		return
	}
	username := strings.TrimSpace(string(usernameBytes))

	// ... (Username validation logic - same as in handleConnection)
	if username == "" {
		sendToWsClient("BAD_USERNAME\nUsername cannot be empty.")
		return
	}
	if len(username) >= USERNAME_MAX_LEN {
		sendToWsClient("BAD_USERNAME\nUsername too long.")
		return
	}
	if !isUsernameAllowed(username) {
		sendToWsClient("NOT_ALLOWED\nUsername not on allowed list.")
		return
	}
	// Check if username is already in use (needs access to global clients map)
	clientsMutex.RLock()
	alreadyExists := false
	for _, existingClient := range clients { // This assumes 'clients' can hold WebSocket clients or a common type
		if existingClient.active && existingClient.username == username {
			alreadyExists = true
			break
		}
	}
	clientsMutex.RUnlock()
	if alreadyExists {
		sendToWsClient("BAD_USERNAME\nUsername already in use.")
		return
	}

	client.username = username
	client.active = true
	// TODO: Add this client to a global map for broadcasting and management
	// This is a critical part for full functionality.
	// For now, we proceed with the single client's lifecycle.
	// Example:
	// client.conn = &wsNetConn{ws: wsConn, remote: net.TCPAddrFromAddr(wsConn.RemoteAddr())} // Wrap wsConn
	// clientsMutex.Lock()
	// clients[client.conn] = client
	// clientsMutex.Unlock()

	log.Printf("WebSocket: User '%s' (allowed) logged in from %s.", username, remoteAddr)
	sendToWsClient(fmt.Sprintf("Welcome, %s!", username))

	// Send recent history
	chatHistoryMutex.Lock()
	if len(chatHistory) > 0 {
		sendToWsClient("--- Recent Chat History ---")
		for _, histMsg := range chatHistory {
			sendToWsClient(histMsg) // histMsg already has newline
		}
		sendToWsClient("--- End of History ---")
	}
	chatHistoryMutex.Unlock()

	joinMsg := fmt.Sprintf("System: %s has joined the chat.\n", username)
	logChatMessage(joinMsg)
	addMessageToHistory(joinMsg)
	// broadcastMessage(joinMsg, client.conn) // Needs adapted broadcast
	broadcastWebSocketMessage(joinMsg, wsConn) // Broadcast to others, excluding self

	// Message processing loop
	for {
		msgType, p, err := wsConn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				log.Printf("WebSocket: Client %s (user: %s) disconnected normally.", remoteAddr, client.username)
			} else if errors.Is(err, io.EOF) {
				log.Printf("WebSocket: Client %s (user: %s) EOF.", remoteAddr, client.username)
			} else {
				log.Printf("WebSocket: Error reading from %s (user: %s): %v", remoteAddr, client.username, err)
			}
			break // Exit loop, defer will handle cleanup
		}

		if msgType != websocket.MessageText {
			log.Printf("WebSocket: Received non-text message from %s (user: %s). Ignoring.", remoteAddr, client.username)
			continue
		}

		message := string(p)
		fullMessageCmd := strings.TrimSpace(message) // For parsing command
		// rawMessageWithNewline := message // Retain original for broadcasting

		log.Printf("WebSocket: Received from %s: %s", client.username, fullMessageCmd)

		// ... (Command parsing logic: PRIVMSG, GROUPMSG, Global - similar to handleConnection)
		// This part needs to be carefully adapted.
		// sendToClient calls need to become sendToWsClient.
		// Broadcasts need to go to both TCP and WebSocket clients.

		if strings.HasPrefix(fullMessageCmd, "PRIVMSG ") {
			// ... (DM logic, find recipient (could be TCP or WS), send message) ...
			// This requires a unified way to find and send to clients.
			sendToWsClient("System: DM processing not fully implemented for WS yet.\n")
		} else if strings.HasPrefix(fullMessageCmd, "GROUPMSG ") {
			// ... (Group message logic) ...
			sendToWsClient("System: GroupMSG processing not fully implemented for WS yet.\n")
		} else { // Global message
			globalMsg := fmt.Sprintf("%s: %s", client.username, message) // message might need newline adjustment
			if !strings.HasSuffix(globalMsg, "\n") {
				globalMsg += "\n"
			}
			logChatMessage(strings.TrimSuffix(globalMsg, "\n"))
			addMessageToHistory(strings.TrimSuffix(globalMsg, "\n"))
			// broadcastMessage(globalMsg, client.conn) // Needs adapted broadcast
			broadcastWebSocketMessage(globalMsg, nil) // Broadcast to ALL WS clients (and ideally TCP too)
		}
	}
}

// TODO: This is a placeholder. A proper implementation requires refactoring
// the global 'clients' map and ClientInfo to handle both TCP and WebSocket clients.
var wsClients = make(map[*websocket.Conn]*ClientInfo) // Temporary, illustrative
var wsClientsMutex sync.RWMutex

func broadcastWebSocketMessage(message string, excludeConn *websocket.Conn) {
	wsClientsMutex.RLock()
	defer wsClientsMutex.RUnlock()

	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}

	for conn, client := range wsClients {
		if client.active && conn != excludeConn {
			// Assuming client.username is set
			err := conn.Write(context.Background(), websocket.MessageText, []byte(message))
			if err != nil {
				log.Printf("WebSocket: Error broadcasting to %s: %v", client.username, err)
				// Consider removing client on repeated errors
			}
		}
	}
	// Also, iterate over TCP clients and send to them
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	for _, tcpClient := range clients {
		if tcpClient.active { // How to exclude if excludeConn was a TCP conn?
			// This shows the complexity of a mixed broadcast.
			// For now, this placeholder only broadcasts to WS clients.
			// A proper solution needs a unified client list or two separate loops.
			if _, ok := tcpClient.conn.(net.Conn); ok { // Check if it's a TCP client
				// sendToClient(tcpClient, message) // This is the existing function for TCP
			}
		}
	}
	log.Printf("Placeholder: Broadcasted to WS clients: %s", strings.TrimSpace(message))
}

// In handleWebSocketConnection, after successful login:
// wsClientsMutex.Lock()
// wsClients[wsConn] = client // Add to our temporary map
// wsClientsMutex.Unlock()
//
// In the defer func of handleWebSocketConnection:
// wsClientsMutex.Lock()
// delete(wsClients, wsConn)
// wsClientsMutex.Unlock()

func startWebServer(serveWeb bool, webPath string, httpPort string) {
	if !serveWeb {
		return
	}

	absWebPath, err := filepath.Abs(webPath)
	if err != nil {
		log.Printf("Error getting absolute path for web client files: %v. Web server not started.", err)
		return
	}
	if _, err := os.Stat(absWebPath); os.IsNotExist(err) {
		log.Printf("Web client directory '%s' not found. Web server not started.", absWebPath)
		log.Printf("Please ensure your web client files (index.html, etc.) are in: %s", absWebPath)
		return
	}

	mux := http.NewServeMux() // Create a new ServeMux
	fileServer := http.FileServer(http.Dir(absWebPath))
	mux.Handle("/", fileServer)    // Serve static files
	mux.HandleFunc("/ws", serveWs) // Handle WebSocket connections on /ws

	log.Printf("Starting HTTP server for web client on port %s, serving files from %s", httpPort, absWebPath)
	log.Printf("WebSocket endpoint available at ws://<host>%s/ws", httpPort)

	go func() {
		// Use the mux with ListenAndServe
		if err := http.ListenAndServe(httpPort, mux); err != nil {
			if err != http.ErrServerClosed {
				log.Printf("HTTP server ListenAndServe error: %v", err)
			} else {
				log.Println("HTTP server closed.")
			}
		}
	}()
}

// Start is the main entry point for the server
func Start(serveWebClient bool) { // Changed from main
	log.SetFlags(log.LstdFlags | log.Lshortfile) // Setup logging

	// Start the web server if requested
	// Use the global webClientPath and httpServerPort or make them configurable
	startWebServer(serveWebClient, webClientPath, httpServerPort)

	// Ensure config directory and files exist or provide clear errors
	// For now, we rely on them being present as per loadAllowedUsers/loadGroups
	if _, err := os.Stat("config"); os.IsNotExist(err) {
		log.Println(
			"Warning: 'config' directory not found in current working directory. User and group files may not load.",
		)
		log.Println(
			"Please ensure 'config/users.txt' and 'config/groups.txt' exist relative to where the server is run.",
		)
		// Optionally, create the directory:
		// if err := os.MkdirAll("config", 0755); err != nil {
		//    log.Fatalf("Failed to create config directory: %v", err)
		// }
	}

	loadAllowedUsers()
	loadGroups()

	var err error
	chatLogFileHandler, err = os.OpenFile(
		CHAT_LOG_FILE,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		log.Fatalf("Error opening chat log file %s: %v", CHAT_LOG_FILE, err)
	}
	defer chatLogFileHandler.Close()

	listener, err := net.Listen("tcp", PORT)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", PORT, err)
	}
	defer listener.Close()
	log.Printf("Server listening for connections on port %s...", PORT)

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Check if the error is due to the listener being closed, e.g., during shutdown
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				log.Printf("Temporary error accepting connection: %v; retrying...", err)
				time.Sleep(time.Second) // Avoid busy-looping on temporary errors
				continue
			}
			// If it's not a temporary error, it might be serious (e.g. listener closed)
			log.Printf("Failed to accept connection: %v", err)
			// If listener.Close() was called, this loop will break.
			// For other critical errors, we might need a way to signal shutdown.
			// For now, if it's a non-temporary error, we might be in a bad state.
			// Consider if this indicates the server should stop.
			// If the error is "use of closed network connection", it means listener was closed.
			if strings.Contains(err.Error(), "use of closed network connection") {
				log.Println("Listener closed, shutting down accept loop.")
				break
			}
			continue
		}
		go handleConnection(conn)
	}
}
