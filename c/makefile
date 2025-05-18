# Compiler and Base Flags
CC = zig cc
CFLAGS_BASE = -std=c11 # Common flags for all C compilations
# For more warnings, you can add: -Wall -Wextra -pedantic
# For optimization, you can add: -O2 or -OReleaseSmall etc.

# Include Directories (relative to this Makefile)
COMMON_INC_DIR = common
CLIENT_CORE_INC_DIR = clients # Where client_core.h is

# CFLAGS with includes
CFLAGS = $(CFLAGS_BASE) -I$(COMMON_INC_DIR) -I$(CLIENT_CORE_INC_DIR)

# Target-Specific Flags
LDFLAGS_WINDOWS = -lws2_32
LDFLAGS_LINUX =   # No specific linker flags needed for basic sockets on Linux by default

# Target Triples
TARGET_LINUX = x86_64-linux-gnu
TARGET_WINDOWS = x86_64-windows-gnu

# Source Directories
SERVER_SRC_DIR = server
CLIENT_WIN_SRC_DIR = clients/windows
CLIENT_CORE_SRC_DIR = clients # Where client_core.c is

# Output Directory
OUTPUT_DIR = bin
OBJ_DIR = $(OUTPUT_DIR)/obj # Directory for object files

# Executable Names
SERVER_NAME_LINUX = server_linux
SERVER_NAME_WINDOWS = server_windows.exe
CLIENT_WIN_NAME = tincan_windows.exe

# Full Paths to Executables
SERVER_LINUX_EXE = $(OUTPUT_DIR)/$(SERVER_NAME_LINUX)
SERVER_WINDOWS_EXE = $(OUTPUT_DIR)/$(SERVER_NAME_WINDOWS)
CLIENT_WINDOWS_EXE = $(OUTPUT_DIR)/$(CLIENT_WIN_NAME)

# Source Files
SERVER_SRC = $(SERVER_SRC_DIR)/server.c
CLIENT_WIN_SRC = $(CLIENT_WIN_SRC_DIR)/win_client.c
CLIENT_CORE_SRC = $(CLIENT_CORE_SRC_DIR)/client_core.c

# Object Files
CLIENT_CORE_OBJ_WIN = $(OBJ_DIR)/client_core_win.o
CLIENT_WIN_OBJ = $(OBJ_DIR)/win_client.o
# If you make a Linux native client using client_core:
# CLIENT_CORE_OBJ_LINUX = $(OBJ_DIR)/client_core_linux.o
# CLIENT_LINUX_OBJ = $(OBJ_DIR)/linux_client.o


# Common Headers (as dependencies to trigger rebuilds if they change)
COMMON_SOCKETS_HEADER = $(COMMON_INC_DIR)/sockets.h
CLIENT_CORE_HEADER = $(CLIENT_CORE_INC_DIR)/client_core.h

# Default target: build all specified executables
all: $(SERVER_LINUX_EXE) $(SERVER_WINDOWS_EXE) $(CLIENT_WINDOWS_EXE)

# Rule to create the output and object directories
$(OUTPUT_DIR) $(OBJ_DIR):
	@mkdir -p $(OUTPUT_DIR)
	@mkdir -p $(OBJ_DIR)
	@echo "Created directory $(@)"

# --- Server Build Rules ---
# Server for Linux
$(SERVER_LINUX_EXE): $(SERVER_SRC) $(COMMON_SOCKETS_HEADER) | $(OUTPUT_DIR)
	@echo "Building Server for Linux..."
	$(CC) -target $(TARGET_LINUX) $(CFLAGS) -o $@ $< $(LDFLAGS_LINUX)

# Server for Windows
$(SERVER_WINDOWS_EXE): $(SERVER_SRC) $(COMMON_SOCKETS_HEADER) | $(OUTPUT_DIR)
	@echo "Building Server for Windows..."
	$(CC) -target $(TARGET_WINDOWS) $(CFLAGS) -o $@ $< $(LDFLAGS_WINDOWS)

# --- Client Core Object File Rules ---
# client_core.o for Windows target
$(CLIENT_CORE_OBJ_WIN): $(CLIENT_CORE_SRC) $(CLIENT_CORE_HEADER) $(COMMON_SOCKETS_HEADER) | $(OBJ_DIR)
	@echo "Compiling client_core.c for Windows..."
	$(CC) -target $(TARGET_WINDOWS) $(CFLAGS) -c $< -o $@

# --- Windows Native Client Build Rules ---
# win_client.o (depends on client_core.h)
$(CLIENT_WIN_OBJ): $(CLIENT_WIN_SRC) $(CLIENT_CORE_HEADER) | $(OBJ_DIR)
	@echo "Compiling win_client.c for Windows..."
	$(CC) -target $(TARGET_WINDOWS) $(CFLAGS) -c $< -o $@

# Linking Windows Client
$(CLIENT_WINDOWS_EXE): $(CLIENT_WIN_OBJ) $(CLIENT_CORE_OBJ_WIN) | $(OUTPUT_DIR)
	@echo "Linking Windows Client..."
	$(CC) -target $(TARGET_WINDOWS) $(CFLAGS) -o $@ $^ $(LDFLAGS_WINDOWS)
	@echo "Windows Client built: $@"


# --- Phony Targets ---
.PHONY: all clean server server_linux server_windows client_windows

# Convenience targets
server: server_linux server_windows
server_linux: $(SERVER_LINUX_EXE)
server_windows: $(SERVER_WINDOWS_EXE)
client_windows: $(CLIENT_WINDOWS_EXE)

clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(OUTPUT_DIR)
	@echo "Clean complete."

# Explanation of some Makefile syntax used:
# $@: The filename of the target.
# $<: The filename of the first prerequisite.
# $^: The filenames of all prerequisites.
# -c: Compile only (produce object file, don't link).
# -I$(DIR): Add DIR to include search path.
# | $(DIR): Order-only prerequisite. Ensures $(DIR) exists.

