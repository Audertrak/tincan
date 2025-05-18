#ifndef CLIENT_CORE_H
#define CLIENT_CORE_H

#include "../../common/sockets.h" // For socket_t and basic socket functions if needed directly
// Or abstract away socket_t if core doesn't need it exposed.

#define CORE_BUFFER_SIZE 1024
#define CORE_USERNAME_MAX_LEN 50
#define CORE_GROUPNAME_MAX_LEN 50

// --- Callback Function Pointer Types ---
// Called when a connection status changes
typedef void (*client_core_on_status_change_cb)(const char *status_message);
// Called when any message (global, DM, system, history) is received from server
typedef void (*client_core_on_message_received_cb)(const char *message_line);
// Called when the server specifically requests username input
typedef void (*client_core_on_username_requested_cb)(void);

// --- Public API Functions ---

// Initializes the client core. Sets up callbacks.
// Returns 0 on success, -1 on error.
int client_core_init(client_core_on_status_change_cb on_status_cb,
                     client_core_on_message_received_cb on_message_cb,
                     client_core_on_username_requested_cb on_username_req_cb);

// Connects to the server.
// Returns 0 on success, -1 on error.
// Asynchronous in nature; status updates via on_status_cb.
int client_core_connect(const char *ip, int port);

// Sends the chosen username to the server.
// Should be called after on_username_req_cb is invoked.
int client_core_send_username(const char *username);

// Sends a global chat message.
int client_core_send_global_message(const char *message);

// Sends a direct message.
int client_core_send_dm(const char *recipient, const char *message);

// Sends a group message.
int client_core_send_group_message(const char *groupname, const char *message);

// Call this function periodically (e.g., in a loop or driven by UI events)
// to process any incoming messages from the server.
// It will trigger the registered callbacks when messages are received.
// Returns: 0 if processed normally, -1 if connection lost or critical error.
int client_core_process_incoming();

// Disconnects from the server and cleans up.
void client_core_disconnect();

// Cleans up any global resources used by the client core.
void client_core_cleanup();

// Utility to check if connected (optional, can be managed via status callback)
// int client_core_is_connected();

#endif // CLIENT_CORE_H
