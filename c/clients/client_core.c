#include "client_core.h"
#include <stdio.h>  // For printf in debug/status messages, perror
#include <stdlib.h> // For malloc, free (if we were to dynamically allocate more)
#include <string.h> // For strcmp, strncpy, strlen, etc.

// --- Module-level (static) variables ---
static socket_t g_client_socket = INVALID_SOCKET;
static char g_server_ip[40]; // Max IP string length (IPv6)
static int g_server_port = 0;
static int g_is_connected = 0;
static int g_login_phase_complete = 0; // To track if username handshake is done

// Buffers for sending/receiving
static char g_send_buffer[CORE_BUFFER_SIZE];
static char g_recv_buffer[CORE_BUFFER_SIZE];

// Callbacks
static client_core_on_status_change_cb g_on_status_cb = NULL;
static client_core_on_message_received_cb g_on_message_cb = NULL;
static client_core_on_username_requested_cb g_on_username_req_cb = NULL;

// --- Helper Functions (static) ---

// (Re-using a similar recv_line from previous client, adapted for core)
// Helper function to receive a line (ends with \n)
// Returns number of bytes read, 0 on EOF, -1 on error
static int core_recv_line(socket_t sock, char *buf, int max_len) {
  int total_received = 0;
  char ch = 0;
  if (sock == INVALID_SOCKET)
    return -1;

  while (total_received < max_len - 1) {
    int n = recv(sock, &ch, 1, 0);
    if (n > 0) {
      buf[total_received++] = ch;
      if (ch == '\n') {
        break;
      }
    } else if (n == 0) { // Connection closed by peer
      if (total_received == 0)
        return 0; // EOF at start
      else
        break; // EOF after some data
    } else {   // Error
#ifdef _WIN32
      if (socket_errno == WSAEWOULDBLOCK)
        return -2; // Non-blocking would return this
#else
      if (socket_errno == EAGAIN || socket_errno == EWOULDBLOCK)
        return -2; // Non-blocking
#endif
      // print_socket_error("core_recv_line failed"); // Handled by caller
      return -1;
    }
  }
  buf[total_received] = '\0';
  return total_received;
}

// Helper function to send data fully
static int core_send_full(socket_t sock, const char *buf, int len) {
  if (sock == INVALID_SOCKET)
    return -1;
  int total_sent = 0;
  while (total_sent < len) {
    int sent_this_call = send(sock, buf + total_sent, len - total_sent, 0);
    if (sent_this_call <= 0) {
      // print_socket_error("core_send_full failed"); // Handled by caller
      return sent_this_call;
    }
    total_sent += sent_this_call;
  }
  return total_sent;
}

static void invoke_status_cb(const char *message) {
  if (g_on_status_cb) {
    g_on_status_cb(message);
  } else {
    printf("CoreStatus: %s\n", message); // Fallback if no callback
  }
}

static void invoke_message_cb(const char *message) {
  if (g_on_message_cb) {
    g_on_message_cb(message);
  } else {
    printf("CoreMsg: %s", message); // Fallback (message includes newline)
  }
}

static void invoke_username_req_cb() {
  if (g_on_username_req_cb) {
    g_on_username_req_cb();
  } else {
    printf("CoreEvent: Server requests username.\n"); // Fallback
  }
}

// --- Public API Function Implementations ---

int client_core_init(client_core_on_status_change_cb on_status_cb,
                     client_core_on_message_received_cb on_message_cb,
                     client_core_on_username_requested_cb on_username_req_cb) {
  socket_init(); // Initialize socket environment (WSAStartup etc.)
  g_on_status_cb = on_status_cb;
  g_on_message_cb = on_message_cb;
  g_on_username_req_cb = on_username_req_cb;
  g_client_socket = INVALID_SOCKET;
  g_is_connected = 0;
  g_login_phase_complete = 0;
  invoke_status_cb("Client core initialized.");
  return 0;
}

int client_core_connect(const char *ip, int port) {
  if (g_is_connected) {
    invoke_status_cb("Already connected.");
    return 0; // Or an error indicating already connected
  }

  g_client_socket = socket(AF_INET, SOCK_STREAM, 0);
  if (g_client_socket == INVALID_SOCKET) {
    print_socket_error("client_core_connect: socket() failed");
    invoke_status_cb("Connection failed: Could not create socket.");
    return -1;
  }

  strncpy(g_server_ip, ip, sizeof(g_server_ip) - 1);
  g_server_ip[sizeof(g_server_ip) - 1] = '\0';
  g_server_port = port;

  struct sockaddr_in server_addr;
  server_addr.sin_family = AF_INET;
  server_addr.sin_port = htons(port);
  server_addr.sin_addr.s_addr = inet_addr(ip);

  if (connect(g_client_socket, (struct sockaddr *)&server_addr,
              sizeof(server_addr)) < 0) {
    print_socket_error("client_core_connect: connect() failed");
    invoke_status_cb("Connection failed: Could not connect to server.");
    close_socket(g_client_socket);
    g_client_socket = INVALID_SOCKET;
    return -1;
  }

  g_is_connected = 1;
  g_login_phase_complete = 0; // Reset login phase
  char status_msg[100];
  snprintf(status_msg, sizeof(status_msg), "Connected to %s:%d.", ip, port);
  invoke_status_cb(status_msg);

  // After connecting, the server should send REQ_USERNAME or SERVER_FULL
  // This will be handled by client_core_process_incoming()
  return 0;
}

int client_core_send_username(const char *username) {
  if (!g_is_connected || g_login_phase_complete) {
    invoke_status_cb(
        "Cannot send username: Not connected or login already complete.");
    return -1;
  }
  if (username == NULL || strlen(username) == 0 ||
      strlen(username) >= CORE_USERNAME_MAX_LEN) {
    invoke_status_cb("Invalid username provided to core.");
    return -1;
  }

  snprintf(g_send_buffer, sizeof(g_send_buffer), "%s\n", username);
  if (core_send_full(g_client_socket, g_send_buffer, strlen(g_send_buffer)) <=
      0) {
    print_socket_error("client_core_send_username: send_full failed");
    invoke_status_cb("Failed to send username to server.");
    client_core_disconnect(); // Disconnect on send failure during handshake
    return -1;
  }
  // Server's response (Welcome, BAD_USERNAME, NOT_ALLOWED) will be handled by
  // process_incoming
  return 0;
}

int client_core_send_global_message(const char *message) {
  if (!g_is_connected || !g_login_phase_complete) {
    invoke_status_cb("Cannot send message: Not connected or not logged in.");
    return -1;
  }
  if (message == NULL || strlen(message) == 0)
    return 0; // Don't send empty

  // Assume message already has newline if needed by protocol, or add it
  snprintf(g_send_buffer, sizeof(g_send_buffer), "%s\n", message);
  if (core_send_full(g_client_socket, g_send_buffer, strlen(g_send_buffer)) <=
      0) {
    print_socket_error("client_core_send_global_message: send_full failed");
    invoke_status_cb("Failed to send global message.");
    client_core_disconnect();
    return -1;
  }
  return 0;
}

int client_core_send_dm(const char *recipient, const char *message) {
  if (!g_is_connected || !g_login_phase_complete) {
    invoke_status_cb("Cannot send DM: Not connected or not logged in.");
    return -1;
  }
  if (recipient == NULL || strlen(recipient) == 0 || message == NULL ||
      strlen(message) == 0) {
    return -1; // Invalid args
  }

  snprintf(g_send_buffer, sizeof(g_send_buffer), "PRIVMSG %s %s\n", recipient,
           message);
  if (core_send_full(g_client_socket, g_send_buffer, strlen(g_send_buffer)) <=
      0) {
    print_socket_error("client_core_send_dm: send_full failed");
    invoke_status_cb("Failed to send direct message.");
    client_core_disconnect();
    return -1;
  }
  return 0;
}

int client_core_send_group_message(const char *groupname, const char *message) {
  if (!g_is_connected || !g_login_phase_complete) {
    invoke_status_cb(
        "Cannot send group message: Not connected or not logged in.");
    return -1;
  }
  if (groupname == NULL || strlen(groupname) == 0 || message == NULL ||
      strlen(message) == 0) {
    return -1; // Invalid args
  }

  snprintf(g_send_buffer, sizeof(g_send_buffer), "GROUPMSG %s %s\n", groupname,
           message);
  if (core_send_full(g_client_socket, g_send_buffer, strlen(g_send_buffer)) <=
      0) {
    print_socket_error("client_core_send_group_message: send_full failed");
    invoke_status_cb("Failed to send group message.");
    client_core_disconnect();
    return -1;
  }
  return 0;
}

int client_core_process_incoming() {
  if (!g_is_connected) {
    return 0; // Nothing to process if not connected
  }

  // For a non-blocking UI, we'd ideally use select() here too, or make the
  // socket non-blocking. For simplicity in this step, core_recv_line will block
  // if no data. A real UI would call this from a separate thread or use
  // non-blocking sockets. For WASM, the "main loop" will call this.

  int len =
      core_recv_line(g_client_socket, g_recv_buffer, sizeof(g_recv_buffer));

  if (len > 0) {
    // g_recv_buffer is null-terminated and includes the newline.
    char temp_line[CORE_BUFFER_SIZE]; // For manipulation without affecting
                                      // original
    strncpy(temp_line, g_recv_buffer, sizeof(temp_line) - 1);
    temp_line[sizeof(temp_line) - 1] = '\0';
    temp_line[strcspn(temp_line, "\r\n")] = 0; // Cleaned version for strcmp

    if (!g_login_phase_complete) { // Handling initial server responses
      if (strcmp(temp_line, "REQ_USERNAME") == 0) {
        invoke_username_req_cb();
      } else if (strcmp(temp_line, "SERVER_FULL") == 0) {
        invoke_message_cb(g_recv_buffer); // Pass full message
        client_core_disconnect();
        return -1; // Indicate connection ended by server
      } else if (strncmp(temp_line, "Welcome, ", 9) == 0) {
        g_login_phase_complete = 1;
        invoke_message_cb(g_recv_buffer); // Pass full welcome message
      } else if (strncmp(temp_line, "BAD_USERNAME", 12) == 0 ||
                 strncmp(temp_line, "NOT_ALLOWED", 11) == 0) {
        invoke_message_cb(g_recv_buffer); // Pass full error message
        client_core_disconnect();
        return -1; // Indicate connection ended by server
      } else {
        // Potentially history or other messages before login fully complete
        invoke_message_cb(g_recv_buffer);
      }
    } else { // Login phase complete, regular messages
      invoke_message_cb(g_recv_buffer);
    }
  } else if (len == 0) { // Server closed connection
    invoke_status_cb("Disconnected: Server closed connection.");
    client_core_disconnect();
    return -1;
  } else if (len == -1) { // Error
    print_socket_error("client_core_process_incoming: recv_line error");
    invoke_status_cb("Disconnected: Network error.");
    client_core_disconnect();
    return -1;
  }
  // len == -2 would be for non-blocking, not handled here yet.
  return 0;
}

void client_core_disconnect() {
  if (g_is_connected) {
    invoke_status_cb("Disconnecting from server...");
  }
  if (g_client_socket != INVALID_SOCKET) {
    close_socket(g_client_socket);
    g_client_socket = INVALID_SOCKET;
  }
  g_is_connected = 0;
  g_login_phase_complete = 0;
  // Don't call invoke_status_cb("Disconnected.") here, as it might be called
  // due to an error where a more specific status was already given.
  // The caller of disconnect or process_incoming should handle final status.
}

void client_core_cleanup() {
  client_core_disconnect();
  socket_cleanup(); // From sockets.h
  invoke_status_cb("Client core cleaned up.");
  // Reset callbacks to NULL
  g_on_status_cb = NULL;
  g_on_message_cb = NULL;
  g_on_username_req_cb = NULL;
}
