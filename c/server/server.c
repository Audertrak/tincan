#include "sockets.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
// winsock2.h (included via sockets.h) should be sufficient for select on
// Windows
#else
#include <sys/time.h> // For fd_set, select, FD_ZERO, FD_SET, FD_CLR, FD_ISSET
#include <sys/types.h>
// #include <unistd.h> // Not strictly needed for this example yet
#endif

#define PORT 8080
#define BUFFER_SIZE 1024
#define MAX_CLIENTS 30
#define USERNAME_MAX_LEN 50
#define CHAT_LOG_FILE "chat_log.txt"
#define MAX_HISTORY_LINES 20
#define ALLOWED_USERS_FILE "confg/users.txt" // Using your filename
#define MAX_ALLOWED_USERS 100
#define GROUPS_FILE "config/groups.txt" // New
#define MAX_GROUPS 20                   // Max number of groups
#define MAX_MEMBERS_PER_GROUP 20        // Max members per group definition
#define GROUPNAME_MAX_LEN 50 // Matches USERNAME_MAX_LEN for simplicity

// Structure to hold client information
typedef struct {
  socket_t socket;
  char username[USERNAME_MAX_LEN];
  struct sockaddr_in address;
  int active; // 0 if slot is free/pending username, 1 if fully active
} client_info_t;

// Structure for group information
typedef struct {
  char name[GROUPNAME_MAX_LEN];
  char members[MAX_MEMBERS_PER_GROUP][USERNAME_MAX_LEN];
  int num_members;
} group_info_t;

// Global arrays
char g_allowed_usernames[MAX_ALLOWED_USERS][USERNAME_MAX_LEN];
int g_num_allowed_users = 0;
group_info_t g_groups[MAX_GROUPS];
int g_num_groups = 0;

// Helper function to duplicate a string (like POSIX strdup)
char *my_strdup(const char *s) {
  if (s == NULL) {
    return NULL;
  }
  size_t len = strlen(s) + 1; // +1 for the null terminator
  char *new_s = (char *)malloc(len);
  if (new_s == NULL) {
    perror("my_strdup: malloc failed");
    return NULL;
  }
  memcpy(new_s, s, len);
  return new_s;
}

// Function to get current timestamp as string
void get_timestamp(char *ts_buffer, size_t len) {
  time_t rawtime;
  struct tm *timeinfo;
  time(&rawtime);
  timeinfo = localtime(&rawtime);
  strftime(ts_buffer, len, "%Y-%m-%d %H:%M:%S", timeinfo);
}

// Function to log a message to the chat file
void log_message(const char *message) {
  FILE *log_file = fopen(CHAT_LOG_FILE, "a");
  if (log_file == NULL) {
    perror("Error opening chat log file");
    return;
  }
  char timestamp[30];
  get_timestamp(timestamp, sizeof(timestamp));
  fprintf(log_file, "[%s] %s", timestamp,
          message); // Assume message has newline
  fclose(log_file);
}

// Function to load allowed usernames from file
void load_allowed_users() {
  FILE *file = fopen(ALLOWED_USERS_FILE, "r");
  if (file == NULL) {
    printf("Warning: Could not open %s. No users will be allowed by default.\n",
           ALLOWED_USERS_FILE);
    return;
  }
  char line[USERNAME_MAX_LEN];
  g_num_allowed_users = 0;
  while (fgets(line, sizeof(line), file) != NULL &&
         g_num_allowed_users < MAX_ALLOWED_USERS) {
    line[strcspn(line, "\r\n")] = 0;
    if (strlen(line) > 0) {
      strncpy(g_allowed_usernames[g_num_allowed_users], line,
              USERNAME_MAX_LEN - 1);
      g_allowed_usernames[g_num_allowed_users][USERNAME_MAX_LEN - 1] = '\0';
      g_num_allowed_users++;
    }
  }
  fclose(file);
  printf("Loaded %d allowed usernames from %s.\n", g_num_allowed_users,
         ALLOWED_USERS_FILE);
  for (int i = 0; i < g_num_allowed_users; ++i) {
    printf("  - %s\n", g_allowed_usernames[i]);
  }
}

// Function to check if a username is allowed
int is_username_allowed(const char *username) {
  for (int i = 0; i < g_num_allowed_users; i++) {
    if (strcmp(g_allowed_usernames[i], username) == 0) {
      return 1; // Allowed
    }
  }
  return 0; // Not allowed
}

// Function to load group definitions
void load_groups() {
  FILE *file = fopen(GROUPS_FILE, "r");
  if (file == NULL) {
    printf("Warning: Could not open %s. No groups will be available.\n",
           GROUPS_FILE);
    return;
  }

  char line_buffer[BUFFER_SIZE];
  g_num_groups = 0;

  while (fgets(line_buffer, sizeof(line_buffer), file) != NULL &&
         g_num_groups < MAX_GROUPS) {
    line_buffer[strcspn(line_buffer, "\r\n")] = 0;

    char *group_name_part = strtok(line_buffer, ":");
    char *members_part = strtok(NULL, ""); // Get the rest of the line

    if (group_name_part != NULL && members_part != NULL &&
        strlen(group_name_part) < GROUPNAME_MAX_LEN) {
      strncpy(g_groups[g_num_groups].name, group_name_part,
              GROUPNAME_MAX_LEN - 1);
      g_groups[g_num_groups].name[GROUPNAME_MAX_LEN - 1] = '\0';
      g_groups[g_num_groups].num_members = 0;

      char *member_token = strtok(members_part, ",");
      while (member_token != NULL &&
             g_groups[g_num_groups].num_members < MAX_MEMBERS_PER_GROUP) {
        if (strlen(member_token) < USERNAME_MAX_LEN) {
          strncpy(g_groups[g_num_groups]
                      .members[g_groups[g_num_groups].num_members],
                  member_token, USERNAME_MAX_LEN - 1);
          g_groups[g_num_groups].members[g_groups[g_num_groups].num_members]
                                        [USERNAME_MAX_LEN - 1] = '\0';
          g_groups[g_num_groups].num_members++;
        }
        member_token = strtok(NULL, ",");
      }
      g_num_groups++;
    }
  }
  fclose(file);
  printf("Loaded %d groups from %s.\n", g_num_groups, GROUPS_FILE);
  for (int i = 0; i < g_num_groups; i++) {
    printf("  - Group '%s': %d members\n", g_groups[i].name,
           g_groups[i].num_members);
  }
}

int main() {
  socket_init();
  load_allowed_users();
  load_groups();

  socket_t listen_socket;
  client_info_t clients[MAX_CLIENTS];
  fd_set read_fds;
  fd_set master_fds;
  socket_t max_sd;
  struct sockaddr_in server_addr;
  char buffer[BUFFER_SIZE];
  char message_to_send_clients[BUFFER_SIZE + USERNAME_MAX_LEN +
                               GROUPNAME_MAX_LEN + 30];
  char system_message[USERNAME_MAX_LEN + 100];

  for (int i = 0; i < MAX_CLIENTS; i++) {
    clients[i].active = 0;
    clients[i].socket = 0;
    memset(clients[i].username, 0, USERNAME_MAX_LEN);
  }

  listen_socket = socket(AF_INET, SOCK_STREAM, 0);
  if (listen_socket == INVALID_SOCKET) {
    print_socket_error("Failed to create listening socket");
    socket_cleanup();
    return 1;
  }
  printf("Listening socket created.\n");

#ifndef _WIN32
  int opt = 1;
  if (setsockopt(listen_socket, SOL_SOCKET, SO_REUSEADDR, (char *)&opt,
                 sizeof(opt)) < 0) {
    print_socket_error("setsockopt(SO_REUSEADDR) failed");
    close_socket(listen_socket);
    socket_cleanup();
    return 1;
  }
#endif

  server_addr.sin_family = AF_INET;
  server_addr.sin_addr.s_addr = INADDR_ANY;
  server_addr.sin_port = htons(PORT);

  if (bind(listen_socket, (struct sockaddr *)&server_addr,
           sizeof(server_addr)) < 0) {
    print_socket_error("Bind failed");
    close_socket(listen_socket);
    socket_cleanup();
    return 1;
  }
  printf("Bind successful on port %d.\n", PORT);

  if (listen(listen_socket, 5) < 0) {
    print_socket_error("Listen failed");
    close_socket(listen_socket);
    socket_cleanup();
    return 1;
  }
  printf("Server listening for connections on port %d...\n", PORT);

  FD_ZERO(&master_fds);
  FD_ZERO(&read_fds);
  FD_SET(listen_socket, &master_fds);
  max_sd = listen_socket;
  printf("Waiting for connections...\n");

  while (1) {
    read_fds = master_fds;
    int activity = select(max_sd + 1, &read_fds, NULL, NULL, NULL);

    if (activity < 0) {
#ifdef _WIN32
      if (socket_errno == WSAEINTR) {
        continue;
      }
#else
      if (socket_errno == EINTR) {
        continue;
      }
#endif
      print_socket_error("select() error");
      break;
    }

    if (FD_ISSET(listen_socket, &read_fds)) {
      struct sockaddr_in new_client_addr_temp;
      socklen_t new_client_addr_len_temp = sizeof(new_client_addr_temp);
      socket_t new_socket =
          accept(listen_socket, (struct sockaddr *)&new_client_addr_temp,
                 &new_client_addr_len_temp);

      if (new_socket == INVALID_SOCKET) {
        print_socket_error("accept() failed");
      } else {
        char client_ip_str[INET_ADDRSTRLEN];
        inet_ntop(AF_INET, &new_client_addr_temp.sin_addr, client_ip_str,
                  INET_ADDRSTRLEN);
        printf("New connection attempt from: %s, port: %d (socket %d)\n",
               client_ip_str, ntohs(new_client_addr_temp.sin_port),
               (int)new_socket);

        int client_idx = -1;
        for (int k = 0; k < MAX_CLIENTS; k++) {
          if (clients[k].socket == 0) {
            client_idx = k;
            break;
          }
        }

        if (client_idx == -1) {
          printf("Max clients reached. Rejecting new connection from %s.\n",
                 client_ip_str);
          send(new_socket, "SERVER_FULL\n", strlen("SERVER_FULL\n"), 0);
          close_socket(new_socket);
        } else {
          clients[client_idx].socket = new_socket;
          clients[client_idx].address = new_client_addr_temp;
          clients[client_idx].active = 0;
          memset(clients[client_idx].username, 0, USERNAME_MAX_LEN);

          send(new_socket, "REQ_USERNAME\n", strlen("REQ_USERNAME\n"), 0);
          FD_SET(new_socket, &master_fds);
          if (new_socket > max_sd) {
            max_sd = new_socket;
          }
          printf("Sent REQ_USERNAME to socket %d. Slot %d assigned.\n",
                 (int)new_socket, client_idx);
        }
      }
    }

    for (int i = 0; i < MAX_CLIENTS; i++) {
      socket_t sender_socket = clients[i].socket;
      if (sender_socket == 0 || !FD_ISSET(sender_socket, &read_fds)) {
        continue;
      }

      if (!clients[i].active) { // Username reception phase
        memset(buffer, 0, BUFFER_SIZE);
        int recv_size = recv(sender_socket, buffer, USERNAME_MAX_LEN - 1, 0);

        if (recv_size > 0) {
          buffer[recv_size] = '\0';
          buffer[strcspn(buffer, "\r\n")] = 0;

          if (strlen(buffer) == 0) {
            send(sender_socket, "BAD_USERNAME\nUsername cannot be empty.\n",
                 strlen("BAD_USERNAME\nUsername cannot be empty.\n"), 0);
            FD_CLR(sender_socket, &master_fds);
            close_socket(sender_socket);
            clients[i].socket = 0;
            printf("Client on socket %d (slot %d) sent empty username. "
                   "Connection closed.\n",
                   (int)sender_socket, i);
            continue;
          }

          if (!is_username_allowed(buffer)) {
            printf("Username '%s' from socket %d (slot %d) is not allowed. "
                   "Rejecting.\n",
                   buffer, (int)sender_socket, i);
            send(sender_socket, "NOT_ALLOWED\nUsername not on allowed list.\n",
                 strlen("NOT_ALLOWED\nUsername not on allowed list.\n"), 0);
            FD_CLR(sender_socket, &master_fds);
            close_socket(sender_socket);
            clients[i].socket = 0;
            continue;
          }

          strncpy(clients[i].username, buffer, USERNAME_MAX_LEN - 1);
          clients[i].username[USERNAME_MAX_LEN - 1] = '\0';
          clients[i].active = 1;

          printf("Username '%s' (allowed) received for socket %d (slot %d).\n",
                 clients[i].username, (int)sender_socket, i);

          char welcome_msg[USERNAME_MAX_LEN + 50];
          sprintf(welcome_msg, "Welcome, %s!\n", clients[i].username);
          send(sender_socket, welcome_msg, strlen(welcome_msg), 0);

          FILE *log_file_read = fopen(CHAT_LOG_FILE, "r");
          if (log_file_read != NULL) {
            char history_line_buffer[BUFFER_SIZE + USERNAME_MAX_LEN + 50];
            char *history_lines_ptrs[MAX_HISTORY_LINES];
            int history_line_count = 0;
            int current_history_idx = 0;
            for (int k = 0; k < MAX_HISTORY_LINES; ++k)
              history_lines_ptrs[k] = NULL;
            while (fgets(history_line_buffer, sizeof(history_line_buffer),
                         log_file_read) != NULL) {
              if (history_lines_ptrs[current_history_idx] != NULL)
                free(history_lines_ptrs[current_history_idx]);
              history_lines_ptrs[current_history_idx] =
                  my_strdup(history_line_buffer);
              if (history_lines_ptrs[current_history_idx] == NULL) {
                fprintf(stderr,
                        "Failed to duplicate history line for user %s\n",
                        clients[i].username);
                break;
              }
              current_history_idx =
                  (current_history_idx + 1) % MAX_HISTORY_LINES;
              if (history_line_count < MAX_HISTORY_LINES)
                history_line_count++;
            }
            fclose(log_file_read);
            if (history_line_count > 0) {
              send(sender_socket, "--- Recent Chat History ---\n",
                   strlen("--- Recent Chat History ---\n"), 0);
              for (int k = 0; k < history_line_count; k++) {
                int idx_to_send = (current_history_idx + k) % MAX_HISTORY_LINES;
                if (history_lines_ptrs[idx_to_send] != NULL)
                  send(sender_socket, history_lines_ptrs[idx_to_send],
                       strlen(history_lines_ptrs[idx_to_send]), 0);
              }
              send(sender_socket, "--- End of History ---\n",
                   strlen("--- End of History ---\n"), 0);
            }
            for (int k = 0; k < MAX_HISTORY_LINES; ++k)
              if (history_lines_ptrs[k] != NULL)
                free(history_lines_ptrs[k]);
          }
          snprintf(system_message, sizeof(system_message),
                   "System: %s has joined the chat.\n", clients[i].username);
          log_message(system_message);
          for (int j = 0; j < MAX_CLIENTS; j++)
            if (clients[j].active && clients[j].socket != sender_socket)
              send(clients[j].socket, system_message, strlen(system_message),
                   0);

        } else {
          printf("Failed to receive username or client disconnected from "
                 "socket %d (slot %d).\n",
                 (int)sender_socket, i);
          FD_CLR(sender_socket, &master_fds);
          close_socket(sender_socket);
          clients[i].socket = 0;
        }
      } else { // clients[i].active is true: Chat message, DM, or GM phase
        memset(buffer, 0, BUFFER_SIZE);
        int recv_size = recv(sender_socket, buffer, BUFFER_SIZE - 1, 0);

        if (recv_size > 0) {
          buffer[recv_size] = '\0';

          if (strncmp(buffer, "PRIVMSG ", 8) == 0) {
            char recipient_username[USERNAME_MAX_LEN];
            char *dm_text_start;
            char *first_space = strchr(buffer + 8, ' ');

            if (first_space != NULL) {
              size_t recipient_len = first_space - (buffer + 8);
              if (recipient_len < USERNAME_MAX_LEN && recipient_len > 0) {
                strncpy(recipient_username, buffer + 8, recipient_len);
                recipient_username[recipient_len] = '\0';
                dm_text_start = first_space + 1;

                int recipient_idx = -1;
                for (int k = 0; k < MAX_CLIENTS; k++) {
                  if (clients[k].active &&
                      strcmp(clients[k].username, recipient_username) == 0) {
                    recipient_idx = k;
                    break;
                  }
                }

                if (recipient_idx != -1) {
                  snprintf(message_to_send_clients,
                           sizeof(message_to_send_clients), "(DM from %s): %s",
                           clients[i].username, dm_text_start);
                  send(clients[recipient_idx].socket, message_to_send_clients,
                       strlen(message_to_send_clients), 0);

                  snprintf(message_to_send_clients,
                           sizeof(message_to_send_clients), "(DM to %s): %s",
                           recipient_username, dm_text_start);
                  send(sender_socket, message_to_send_clients,
                       strlen(message_to_send_clients), 0);

                  char dm_log_buffer[BUFFER_SIZE + USERNAME_MAX_LEN * 2 + 20];
                  char temp_dm_text[BUFFER_SIZE];
                  strncpy(temp_dm_text, dm_text_start,
                          sizeof(temp_dm_text) - 1);
                  temp_dm_text[sizeof(temp_dm_text) - 1] = '\0';
                  temp_dm_text[strcspn(temp_dm_text, "\r\n")] = 0;

                  snprintf(dm_log_buffer, sizeof(dm_log_buffer),
                           "DM from %s to %s: %s\n", clients[i].username,
                           recipient_username, temp_dm_text);
                  log_message(dm_log_buffer);
                  printf("DM from %s to %s: %s\n", clients[i].username,
                         recipient_username, temp_dm_text);

                } else {
                  snprintf(message_to_send_clients,
                           sizeof(message_to_send_clients),
                           "System: User '%s' not found or is offline.\n",
                           recipient_username);
                  send(sender_socket, message_to_send_clients,
                       strlen(message_to_send_clients), 0);
                  printf("User %s tried to DM non-existent/offline user %s\n",
                         clients[i].username, recipient_username);
                }
              } else {
                send(sender_socket,
                     "System: Invalid recipient in DM command.\n",
                     strlen("System: Invalid recipient in DM command.\n"), 0);
              }
            } else {
              send(sender_socket,
                   "System: Invalid DM command format from client.\n",
                   strlen("System: Invalid DM command format from client.\n"),
                   0);
            }
          } else if (strncmp(buffer, "GROUPMSG ", 9) == 0) {
            char group_name_req[GROUPNAME_MAX_LEN];
            char *gm_text_start;
            char *first_space = strchr(buffer + 9, ' ');

            if (first_space != NULL) {
              size_t group_name_len = first_space - (buffer + 9);
              if (group_name_len < GROUPNAME_MAX_LEN && group_name_len > 0) {
                strncpy(group_name_req, buffer + 9, group_name_len);
                group_name_req[group_name_len] = '\0';
                gm_text_start = first_space + 1;

                int group_idx = -1;
                for (int g = 0; g < g_num_groups; g++) {
                  if (strcmp(g_groups[g].name, group_name_req) == 0) {
                    group_idx = g;
                    break;
                  }
                }

                if (group_idx != -1) {
                  int members_messaged = 0;
                  snprintf(message_to_send_clients,
                           sizeof(message_to_send_clients), "(#%s from %s): %s",
                           g_groups[group_idx].name, clients[i].username,
                           gm_text_start);

                  for (int m = 0; m < g_groups[group_idx].num_members; m++) {
                    const char *member_username =
                        g_groups[group_idx].members[m];
                    for (int c_idx = 0; c_idx < MAX_CLIENTS; c_idx++) {
                      if (clients[c_idx].active &&
                          strcmp(clients[c_idx].username, member_username) ==
                              0) {
                        send(clients[c_idx].socket, message_to_send_clients,
                             strlen(message_to_send_clients), 0);
                        members_messaged++;
                        break;
                      }
                    }
                  }

                  char confirmation_msg[GROUPNAME_MAX_LEN + BUFFER_SIZE + 30];
                  snprintf(confirmation_msg, sizeof(confirmation_msg),
                           "(To #%s): %s", g_groups[group_idx].name,
                           gm_text_start);
                  send(sender_socket, confirmation_msg,
                       strlen(confirmation_msg), 0);

                  char gm_log_buffer[BUFFER_SIZE + USERNAME_MAX_LEN +
                                     GROUPNAME_MAX_LEN + 30];
                  char temp_gm_text[BUFFER_SIZE];
                  strncpy(temp_gm_text, gm_text_start,
                          sizeof(temp_gm_text) - 1);
                  temp_gm_text[sizeof(temp_gm_text) - 1] = '\0';
                  temp_gm_text[strcspn(temp_gm_text, "\r\n")] = 0;

                  snprintf(gm_log_buffer, sizeof(gm_log_buffer),
                           "GROUPMSG to #%s from %s: %s\n",
                           g_groups[group_idx].name, clients[i].username,
                           temp_gm_text);
                  log_message(gm_log_buffer);
                  printf("GROUPMSG to #%s from %s: %s (%d members messaged)\n",
                         g_groups[group_idx].name, clients[i].username,
                         temp_gm_text, members_messaged);

                } else {
                  snprintf(message_to_send_clients,
                           sizeof(message_to_send_clients),
                           "System: Group '#%s' not found.\n", group_name_req);
                  send(sender_socket, message_to_send_clients,
                       strlen(message_to_send_clients), 0);
                }
              } else {
                send(sender_socket,
                     "System: Invalid group name in GM command.\n",
                     strlen("System: Invalid group name in GM command.\n"), 0);
              }
            } else {
              send(sender_socket,
                   "System: Invalid GM command format from client.\n",
                   strlen("System: Invalid GM command format from client.\n"),
                   0);
            }
          } else { // Global chat message
            printf("Received global from %s (socket %d): %s",
                   clients[i].username, (int)sender_socket, buffer);

            snprintf(message_to_send_clients, sizeof(message_to_send_clients),
                     "%s: %s", clients[i].username, buffer);

            log_message(message_to_send_clients);

            printf("Broadcasting: %s", message_to_send_clients);
            for (int j = 0; j < MAX_CLIENTS; j++) {
              if (clients[j].active) {
                send(clients[j].socket, message_to_send_clients,
                     strlen(message_to_send_clients), 0);
              }
            }
          }
        } else { // recv_size <= 0: Disconnect or error
          char client_ip_str[INET_ADDRSTRLEN];
          inet_ntop(AF_INET, &clients[i].address.sin_addr, client_ip_str,
                    INET_ADDRSTRLEN);
          printf("%s (socket %d, ip %s, slot %d) disconnected.\n",
                 clients[i].username, (int)sender_socket, client_ip_str, i);
          snprintf(system_message, sizeof(system_message),
                   "System: %s has left the chat.\n", clients[i].username);
          log_message(system_message);
          FD_CLR(sender_socket, &master_fds);
          close_socket(sender_socket);
          clients[i].active = 0;
          clients[i].socket = 0;
          memset(clients[i].username, 0, USERNAME_MAX_LEN);
          printf("Broadcasting: %s", system_message);
          for (int j = 0; j < MAX_CLIENTS; j++)
            if (clients[j].active)
              send(clients[j].socket, system_message, strlen(system_message),
                   0);
        }
      }
    }
  }

  // Cleanup (currently unreachable)
  printf("Server shutting down.\n");
  for (int i = 0; i < MAX_CLIENTS; i++) {
    if (clients[i].socket != 0) {
      close_socket(clients[i].socket);
    }
  }
  close_socket(listen_socket);
  socket_cleanup();

  return 0;
}
