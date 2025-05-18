// tincan/cmd/tincan-cli/main.go
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
	"sync" // For a waitgroup to keep main alive while core processes
	"time" // For potential sleep/delays

	"tincan/internal/client/core" // Path to your client core package
)

const (
	ConsoleBufferSize      = core.CoreBufferSize
	ConsoleUsernameMaxLen  = core.CoreUsernameMaxLen
	ConsoleGroupNameMaxLen = core.CoreGroupNameMaxLen
	ServerIP               = "127.0.0.1" // Default server IP
	ServerPort             = 8080        // Default server port
)

var (
	clientCore                 *core.ClientCore
	userInputReader            *bufio.Reader
	isWaitingForUsernamePrompt = false
	isAppRunning               = true
	myUsernameUI               = "" // To store the username for the prompt
	cliMutex                   sync.Mutex
	shutdownWg                 sync.WaitGroup // To wait for core to shutdown cleanly
)

// --- Callback Implementations ---

func consoleOnStatusChange(statusMessage string) {
	fmt.Printf("Status: %s\n", statusMessage)
	if strings.Contains(statusMessage, "Disconnected") ||
		strings.Contains(statusMessage, "Connection failed") ||
		strings.Contains(statusMessage, "Server is full") ||
		strings.Contains(statusMessage, "Login failed") {
		// If a disconnect status is received, signal app to stop
		// isAppRunning = false // This might be too abrupt, let main loop handle it
	}
	// If we are logged in, re-display prompt
	cliMutex.Lock()
	usernameForPrompt := myUsernameUI
	loggedIn := clientCore != nil && clientCore.IsLoggedIn()
	cliMutex.Unlock()

	if loggedIn && usernameForPrompt != "" {
		fmt.Printf("%s> ", usernameForPrompt)
	}
}

func consoleOnMessageReceived(messageLine string) {
	// message_line from core already includes newline
	fmt.Printf("%s", messageLine) // Print the message directly

	// After printing a message, re-display the prompt if user is logged in
	cliMutex.Lock()
	usernameForPrompt := myUsernameUI
	loggedIn := clientCore != nil && clientCore.IsLoggedIn()
	cliMutex.Unlock()

	if loggedIn && usernameForPrompt != "" {
		fmt.Printf("%s> ", usernameForPrompt)
	}
}

func consoleOnUsernameRequested() {
	fmt.Println("Server requests username.")
	cliMutex.Lock()
	isWaitingForUsernamePrompt = true // Signal main loop to prompt for username
	cliMutex.Unlock()
	// Prompt will be handled in the main loop to integrate with fgets
	fmt.Print("Enter username: ") // Initial prompt
}

func consoleOnError(err error, context string) {
	log.Printf("ClientCore Error (%s): %v\n", context, err)
	// Potentially trigger a shutdown or specific UI update based on error
}

func consoleOnLoginSuccess(username string) {
	fmt.Printf("Successfully logged in as: %s\n", username)
	cliMutex.Lock()
	myUsernameUI = username            // Set the username for the prompt
	isWaitingForUsernamePrompt = false // Ensure this is false
	cliMutex.Unlock()
	// Re-display prompt
	fmt.Printf("%s> ", username)
}

func main() {
	log.Println("Starting Tincan CLI client...")
	userInputReader = bufio.NewReader(os.Stdin)

	clientCore = core.NewClientCore(
		consoleOnStatusChange,
		consoleOnMessageReceived,
		consoleOnUsernameRequested,
		consoleOnError,
		consoleOnLoginSuccess, // Added
	)
	shutdownWg.Add(1) // For the clientCore's lifecycle

	// Attempt to connect
	err := clientCore.Connect(ServerIP, ServerPort)
	if err != nil {
		log.Printf("Failed to initiate connection: %v. Exiting.", err)
		clientCore.Cleanup()
		shutdownWg.Done() // Decrement if connect fails before goroutines start
		return
	}

	// Main application loop
	for isAppRunning {
		cliMutex.Lock()
		waitingForUser := isWaitingForUsernamePrompt
		loggedIn := clientCore.IsLoggedIn()
		currentUsername := myUsernameUI
		cliMutex.Unlock()

		if !clientCore.IsConnected() && !loggedIn {
			// If we disconnected and weren't trying to log in, maybe exit
			// Give a small grace period for disconnect messages to print
			time.Sleep(100 * time.Millisecond)
			if !clientCore.IsConnected() { // Check again
				log.Println("Connection lost and not in login phase. Exiting.")
				isAppRunning = false
				break
			}
		}

		if waitingForUser {
			// Prompt is already displayed by callback or previous iteration
			// fmt.Print("Enter username: ") // Redundant if callback did it
		} else if loggedIn && currentUsername != "" {
			fmt.Printf("%s> ", currentUsername)
		} else {
			// Not logged in, not waiting for username prompt (e.g. connecting, or failed)
			// The status callbacks should provide info.
			// To prevent a tight loop if stuck, add a small sleep.
			// Or rely on clientCore.IsConnected() check above.
			time.Sleep(50 * time.Millisecond) // Prevents busy loop if no prompt
		}

		userInput, err := userInputReader.ReadString('\n')
		if err != nil {
			log.Printf("Error reading user input: %v. Exiting.", err)
			isAppRunning = false
			break
		}
		userInput = strings.TrimSpace(userInput)

		if userInput == "" && !waitingForUser { // Allow empty input if not for username
			continue
		}

		cliMutex.Lock()
		if isWaitingForUsernamePrompt {
			// The prompt "Enter username: " is shown by consoleOnUsernameRequested or prior loop.
			cliMutex.Unlock() // Unlock before core call

			if userInput == "" {
				fmt.Println("Username cannot be empty. Server will likely reject.")
			}

			// Send the username. The core will handle server's response.
			// consoleOnLoginSuccess will set myUsernameUI and clear isWaitingForUsernamePrompt.
			// If login fails, status callbacks will indicate, and server might disconnect.
			err := clientCore.SendUsername(userInput)
			if err != nil {
				log.Printf("Error sending username: %v", err)
			}
			// We don't immediately set isWaitingForUsernamePrompt = false here.
			// We let the server's response (handled by callbacks) dictate the next state.
			// For example, if server sends REQ_USERNAME again, the flag will be set true again.
			// If login success, consoleOnLoginSuccess sets it false.
			continue // Go back to process server's response
		}
		cliMutex.Unlock() // Ensure unlock if not in username prompt phase
		// Command parsing (if not waiting for username)
		if strings.HasPrefix(userInput, "/dm ") {
			parts := strings.SplitN(userInput, " ", 3)
			if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
				fmt.Println("System: Invalid DM format. Use: /dm <username> <message>")
				continue
			}
			recipient := parts[1]
			message := parts[2]
			err := clientCore.SendDirectMessage(recipient, message)
			if err != nil {
				log.Printf("Error sending DM: %v", err)
			}
		} else if strings.HasPrefix(userInput, "/gm ") {
			parts := strings.SplitN(userInput, " ", 3)
			if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
				fmt.Println("System: Invalid GM format. Use: /gm <groupname> <message>")
				continue
			}
			groupName := parts[1]
			message := parts[2]
			err := clientCore.SendGroupMessage(groupName, message)
			if err != nil {
				log.Printf("Error sending group message: %v", err)
			}
		} else if userInput == "/exit" || userInput == "/quit" {
			log.Println("Disconnecting...")
			isAppRunning = false
		} else if strings.HasPrefix(userInput, "/") {
			fmt.Println("System: Unknown command.")
		} else { // Global message
			if clientCore.IsLoggedIn() { // Only send if logged in
				err := clientCore.SendGlobalMessage(userInput)
				if err != nil {
					log.Printf("Error sending global message: %v", err)
				}
			} else if clientCore.IsConnected() {
				fmt.Println("System: Please wait for login to complete before sending messages.")
			} else {
				fmt.Println("System: Not connected. Cannot send message.")
			}
		}
	} // end while isAppRunning

	log.Println("CLI client shutting down...")
	clientCore.Disconnect() // Ensure disconnect is called
	clientCore.Cleanup()    // Perform cleanup
	shutdownWg.Done()       // Signal that core is done
	shutdownWg.Wait()       // Wait for any core goroutines (though Disconnect should handle its own)
	log.Println("CLI client exited.")
}
