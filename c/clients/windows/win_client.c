#include <stdio.h>
#include <stdlib.h> // For exit() in case of critical core init failure
#include <string.h>

// Adjust include path based on where client_core.h is relative to win_client.c
// If client_core.h is in 'clients/' and win_client.c is in 'clients/windows/',
// then the path is "../client_core.h"
#include "../client_core.h" // Assuming client_core.h is in the parent 'clients' directory

#define CONSOLE_BUFFER_SIZE                                                    \
  CORE_BUFFER_SIZE // Use same buffer size for consistency
#define CONSOLE_USERNAME_MAX_LEN CORE_USERNAME_MAX_LEN
#define CONSOLE_GROUPNAME_MAX_LEN CORE_GROUPNAME_MAX_LEN

// --- Global state for this console UI ---
static char g_my_username_ui[CONSOLE_USERNAME_MAX_LEN] = {0};
static int g_waiting_for_username_prompt = 0;
static int g_app_running = 1;

// --- Callback Implementations ---

void console_on_status_change(const char *status_message) {
  printf("Status: %s\n", status_message);
  if (strstr(status_message, "Disconnected") != NULL ||
      strstr(status_message, "Connection failed") != NULL) {
    // If a disconnect status is received, we might want to stop the app loop
    // g_app_running = 0; // Or handle this more gracefully
  }
}

void console_on_message_received(const char *message_line) {
  // message_line from core already includes newline
  printf("%s", message_line);
  // After printing a message, re-display the prompt if user is logged in
  if (strlen(g_my_username_ui) > 0 && g_app_running) {
    printf("%s> ", g_my_username_ui);
    fflush(stdout); // Ensure prompt is displayed immediately
  }
}

void console_on_username_requested() {
  printf("Server requests username.\n");
  g_waiting_for_username_prompt = 1; // Signal main loop to prompt for username
  // Prompt will be handled in the main loop to integrate with fgets
}

int main() {
  if (client_core_init(console_on_status_change, console_on_message_received,
                       console_on_username_requested) != 0) {
    fprintf(stderr, "Failed to initialize client core. Exiting.\n");
    return 1;
  }

  // Attempt to connect
  // TODO: Get server IP and Port from config or command line arguments later
  const char *server_ip = "127.0.0.1";
  int server_port = 8080;
  if (client_core_connect(server_ip, server_port) != 0) {
    // Status callback would have printed an error.
    client_core_cleanup();
    return 1;
  }

  char user_input_buffer[CONSOLE_BUFFER_SIZE];

  while (g_app_running) {
    // Process any incoming messages first
    if (client_core_process_incoming() == -1) {
      // Connection lost or critical error, core should have called status_cb
      g_app_running = 0; // Stop the loop
      break;
    }

    if (g_waiting_for_username_prompt) {
      printf("Enter username: ");
      fflush(stdout);
      if (fgets(user_input_buffer, sizeof(user_input_buffer), stdin) == NULL) {
        fprintf(stderr, "Error reading username input.\n");
        g_app_running = 0;
        break;
      }
      user_input_buffer[strcspn(user_input_buffer, "\r\n")] =
          0; // Clean newline

      if (strlen(user_input_buffer) == 0) {
        printf("Username cannot be empty. Please try again.\n");
        // The server will re-request if it was an actual REQ_USERNAME phase
        // Or the user can try again if they just hit enter.
        // For simplicity, we just let client_core_send_username handle empty.
      }

      // Store username for UI prompt, even if core rejects it later
      strncpy(g_my_username_ui, user_input_buffer,
              CONSOLE_USERNAME_MAX_LEN - 1);
      g_my_username_ui[CONSOLE_USERNAME_MAX_LEN - 1] = '\0';

      if (client_core_send_username(user_input_buffer) != 0) {
        // Error sending username, core status_cb should have notified.
        // Might lead to disconnect.
      }
      g_waiting_for_username_prompt = 0; // Reset flag
      // Don't immediately show prompt, wait for server response via
      // on_message_received
      continue; // Go back to process_incoming to get server's response to
                // username
    }

    // Only show prompt if not waiting for username and username is set (meaning
    // login likely succeeded) A more robust check would be a flag set by the
    // core upon successful login. For now, if g_my_username_ui is set, we
    // assume we can show a prompt.
    if (strlen(g_my_username_ui) > 0) {
      printf("%s> ", g_my_username_ui);
      fflush(stdout);
    } else {
      // If username not set yet (e.g. still in REQ_USERNAME phase, or
      // connection failed before that) don't show a user-specific prompt. The
      // process_incoming will handle server messages. We might need a small
      // delay here or a different way to handle the loop if not logged in. For
      // now, this means if connection fails before username prompt, it might
      // loop fast. A better approach is to have client_core_is_connected() or a
      // state from core. Let's assume process_incoming will lead to disconnect
      // if stuck.
    }

    if (fgets(user_input_buffer, sizeof(user_input_buffer), stdin) == NULL) {
      printf("Input error or EOF. Disconnecting.\n");
      g_app_running = 0; // End loop on input error
      break;
    }

    // No need to strip newline here if core functions expect it or handle it.
    // Our core send functions add the newline. So, strip it from user input.
    user_input_buffer[strcspn(user_input_buffer, "\r\n")] = 0;

    if (strlen(user_input_buffer) == 0) { // User just pressed Enter
      continue;
    }

    // Command parsing
    if (strncmp(user_input_buffer, "/dm ", 4) == 0) {
      char recipient[CONSOLE_USERNAME_MAX_LEN];
      char *message_part;
      char *first_space = strchr(user_input_buffer + 4, ' ');

      if (first_space != NULL) {
        size_t recipient_len = first_space - (user_input_buffer + 4);
        if (recipient_len < CONSOLE_USERNAME_MAX_LEN && recipient_len > 0) {
          strncpy(recipient, user_input_buffer + 4, recipient_len);
          recipient[recipient_len] = '\0';
          message_part = first_space + 1;

          if (strlen(message_part) > 0) {
            client_core_send_dm(recipient, message_part);
          } else {
            printf("System: DM message cannot be empty.\n");
          }
        } else {
          printf("System: Invalid recipient username for DM.\n");
        }
      } else {
        printf("System: Invalid DM format. Use: /dm <username> <message>\n");
      }
    } else if (strncmp(user_input_buffer, "/gm ", 4) == 0) {
      char group_name[CONSOLE_GROUPNAME_MAX_LEN];
      char *message_part;
      char *first_space = strchr(user_input_buffer + 4, ' ');
      if (first_space != NULL) {
        size_t group_name_len = first_space - (user_input_buffer + 4);
        if (group_name_len < CONSOLE_GROUPNAME_MAX_LEN && group_name_len > 0) {
          strncpy(group_name, user_input_buffer + 4, group_name_len);
          group_name[group_name_len] = '\0';
          message_part = first_space + 1;

          if (strlen(message_part) > 0) {
            client_core_send_group_message(group_name, message_part);
          } else {
            printf("System: Group message cannot be empty.\n");
          }
        } else {
          printf("System: Invalid group name for GM.\n");
        }
      } else {
        printf("System: Invalid GM format. Use: /gm <groupname> <message>\n");
      }
    } else if (strcmp(user_input_buffer, "/exit") == 0 ||
               strcmp(user_input_buffer, "/quit") == 0) {
      printf("Disconnecting...\n");
      g_app_running = 0; // Signal to exit loop
    } else {             // Global message
      client_core_send_global_message(user_input_buffer);
    }
  } // end while g_app_running

  client_core_disconnect();
  client_core_cleanup();
  printf("Client shut down.\n");
  return 0;
}
