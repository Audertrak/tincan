# tincan

Tincan is a self-hosted, client-server based chat application designed for keeping in touch with friends and family. The project aims for simplicity and control, allowing users to host their own chat servers.

## Features

**Core Functionality (Implemented):**

*   **Server:**
    *   Handles multiple client connections.
    *   User authentication via a server-side `users.txt` configuration file (admin-managed).
    *   Global server text chat with persistent message history (logged to `chat_log.txt`).
    *   Support for group definitions via `groups.txt` for group messaging.
    *   Direct Messaging (DM) between users.
    *   Group Messaging (GM) to predefined groups.
    *   Optionally serves the web client files.
*   **Client Core Logic:**
    *   Shared Go package for client-side communication logic.
*   **CLI Client:**
    *   Connects to the Tincan server.
    *   Supports login, sending/receiving global messages, DMs, and group messages.
*   **Web Client (WASM):**
    *   Connects to the Tincan server via WebSockets.
    *   Basic UI for login, sending/receiving messages.
    *   Built using Go compiled to WebAssembly (WASM).

**Planned Features:**

*   Native desktop client (using Go and Raylib).
*   Android client.
*   iOS client (experimental).
*   Notifications for DMs and mentions.
*   Voice chat support.

## Getting Started

### Prerequisites

*   Go (version 1.20+ recommended)
*   TinyGo (for compiling the WebAssembly client, version 0.30+ recommended)
*   Binaryen (for `wasm-opt`, required by TinyGo for optimized WASM output - ensure `wasm-opt` is in your PATH)

### Configuration

1.  **User Configuration:**
    Create/edit `config/users.txt` in the project root. Add one allowed username per line.
    Example:
    ```txt
    alice
    bob
    charlie
    ```

2.  **Group Configuration:**
    Create/edit `config/groups.txt` in the project root. Define groups with the format `groupname:user1,user2,user3`.
    Example:
    ```txt
    friends:alice,bob
    devs:charlie,alice
    ```

### Building and Running

All commands should be run from the project root directory (`tincan/`).

**1. Server:**

*   **Build:**
    ```bash
    go build -o ./bin/tincan-server ./cmd/tincan-server/main.go
    ```
    (Output will be in `tincan/bin/tincan-server`)

*   **Run (with web client serving enabled by default on port :8081):**
    ```bash
    ./bin/tincan-server
    ```
    Or using `go run`:
    ```bash
    go run ./cmd/tincan-server/main.go
    ```

*   **Run (headless mode, no web client serving):**
    ```bash
    ./bin/tincan-server -serveweb=false
    ```
    Or using `go run`:
    ```bash
    go run ./cmd/tincan-server/main.go -serveweb=false
    ```
    The chat server listens on TCP port `:8080` by default. The web server (if enabled) listens on HTTP port `:8081` by default.

**2. CLI Client:**

*   **Build:**
    ```bash
    go build -o ./bin/tincan-cli ./cmd/tincan-cli/main.go
    ```
    (Output will be in `tincan/bin/tincan-cli`)

*   **Run:**
    Ensure the server is running.
    ```bash
    ./bin/tincan-cli
    ```
    Or using `go run`:
    ```bash
    go run ./cmd/tincan-cli/main.go
    ```

**3. Web (WASM) Client:**

*   **Copy `wasm_exec.js`:**
    This file is required to run Go WASM. Copy it from your TinyGo installation directory to `clients/web/wasm_exec.js`.
    The location is typically `$(tinygo env TINYGOROOT)/targets/wasm_exec.js`.

*   **Build WASM Module:**
    ```bash
    tinygo build -o ./clients/web/tincan.wasm -target wasm ./cmd/tincan-wasm/main.go
    ```
    (Output will be `clients/web/tincan.wasm`)

*   **Access:**
    If the Tincan server is running with web client serving enabled (default), open your browser and navigate to:
    `http://localhost:8081` (or the configured HTTP port for the server).

