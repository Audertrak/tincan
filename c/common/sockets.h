#ifndef SOCKETS_H // Assuming you renamed the include guard to match the
                  // filename
#define SOCKETS_H

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
// #pragma comment(lib, "Ws2_32.lib") // We link explicitly with Zig, so this is
// optional
typedef SOCKET socket_t;
#define close_socket(s) closesocket(s)
#define socket_errno WSAGetLastError()
// INVALID_SOCKET is already defined in winsock2.h
#else                  // Linux/macOS
#include <arpa/inet.h> // For inet_ntop, htons, etc.
#include <errno.h>     // For errno
#include <netinet/in.h>
#include <string.h> // For strerror (needed by print_socket_error on Linux)
#include <sys/socket.h>
#include <unistd.h>         // For close
typedef int socket_t;
#define INVALID_SOCKET (-1) // Define it for POSIX
#define close_socket(s) close(s)
#define socket_errno errno
#endif

#include <stdio.h>
#include <stdlib.h> // For exit()

// Initializes socket environment (e.g., WSAStartup on Windows)
static inline void socket_init(void) {
#ifdef _WIN32
  WSADATA wsaData;
  if (WSAStartup(MAKEWORD(2, 2), &wsaData) != 0) {
    fprintf(stderr, "WSAStartup failed. Error Code: %d\n", socket_errno);
    exit(1);
  }
}
#else
} // No-op on POSIX systems
#endif

// Cleans up socket environment (e.g., WSACleanup on Windows)
static inline void socket_cleanup(void) {
#ifdef _WIN32
  WSACleanup();
}
#else
} // No-op on POSIX systems
#endif

// Prints the last socket error message
static inline void print_socket_error(const char *message) {
#ifdef _WIN32
  fprintf(stderr, "%s: %d\n", message, socket_errno);
#else
  fprintf(stderr, "%s: %s\n", message, strerror(socket_errno));
#endif
}

#endif // SOCKETS_H
